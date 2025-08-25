/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scheduler

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	PluginName            = "Chronos"
	JobDurationAnnotation = "scheduling.workload.io/expected-duration-seconds"
)

type Chronos struct {
	handle           framework.Handle
	batchScheduler   *BatchScheduler
	batchModeEnabled bool
	inFlightPods     sync.Map
}

func New(ctx context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	klog.Infof("Initializing Chronos plugin")

	chronos := &Chronos{
		handle: h,
	}

	// Feature flag: grouping enables batch scheduling, disabled = individual scheduling
	groupingEnabled := os.Getenv("CHRONOS_GROUPING_ENABLED")
	if groupingEnabled == "true" && h != nil {
		chronos.batchModeEnabled = true
		chronos.batchScheduler = NewBatchScheduler(h, chronos)
		chronos.batchScheduler.Start()
		klog.Infof("🚀 Chronos GROUPING MODE enabled - Using batch scheduler")
	} else if groupingEnabled == "true" && h == nil {
		// Test mode: enable batch mode flag but don't create actual batch scheduler
		chronos.batchModeEnabled = true
		klog.Infof("🧪 Chronos GROUPING MODE enabled - Test mode (no actual scheduler)")
	} else {
		chronos.batchModeEnabled = false
		klog.Infof("📝 Chronos INDIVIDUAL MODE enabled - Using traditional pod-by-pod scheduling")
	}

	return chronos, nil
}

func (s *Chronos) Name() string {
	return PluginName
}

func (s *Chronos) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	klog.V(4).Infof("Scoring pod %s/%s for node %s", p.Namespace, p.Name, nodeName)

	newPodDurationStr, ok := p.Annotations[JobDurationAnnotation]
	if !ok {
		return 0, framework.NewStatus(framework.Success)
	}

	// GROUPING MODE: When CHRONOS_GROUPING_ENABLED=true, use batch scheduler
	if s.batchModeEnabled {
		if _, inFlight := s.inFlightPods.Load(p.UID); inFlight {
			klog.V(4).Infof("Pod %s/%s is in-flight with batch scheduler, assigning minimal score.", p.Namespace, p.Name)
			return 1, framework.NewStatus(framework.Success)
		}

		klog.V(4).Infof("Pod %s/%s is a candidate for batch grouping scheduler.", p.Namespace, p.Name)
		return 1, framework.NewStatus(framework.Success)
	}

	// INDIVIDUAL MODE: When CHRONOS_GROUPING_ENABLED=false, use traditional pod-by-pod scoring
	newPodDurationFloat, err := strconv.ParseFloat(newPodDurationStr, 64)
	if err != nil {
		klog.Warningf("Could not parse duration '%s' for pod %s/%s: %v", newPodDurationStr, p.Namespace, p.Name, err)
		return 0, framework.NewStatus(framework.Success)
	}
	newPodDuration := int64(math.Round(newPodDurationFloat))

	nodeInfo, err := s.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		klog.Errorf("Error getting node info for %s: %v", nodeName, err)
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q info: %s", nodeName, err))
	}

	maxRemainingTime := s.calculateMaxRemainingTimeOptimized(nodeInfo.Pods)
	score := s.CalculateOptimizedScore(nodeName, maxRemainingTime, newPodDuration)

	klog.V(4).Infof("Pod: %s/%s, Node: %s, Existing: %ds, NewJob: %ds, Score: %d (individual mode)",
		p.Namespace, p.Name, nodeName, maxRemainingTime, newPodDuration, score)

	return score, framework.NewStatus(framework.Success)
}

// REFINED: Optimized duration parsing by trying integer parsing first.
func (s *Chronos) calculateMaxRemainingTimeOptimized(pods []*framework.PodInfo) int64 {
	if len(pods) == 0 {
		return 0
	}
	maxRemainingTime := int64(0)
	now := time.Now()
	for _, podInfo := range pods {
		pod := podInfo.Pod
		if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
			continue
		}
		durationStr, exists := pod.Annotations[JobDurationAnnotation]
		if !exists {
			continue
		}

		var duration int64
		// Optimization: Try parsing as an integer first, which is much faster.
		// Fall back to float parsing only if necessary.
		val, err := strconv.ParseInt(durationStr, 10, 64)
		if err == nil {
			duration = val
		} else if strings.Contains(durationStr, ".") {
			// Fallback for float values like "300.5"
			fVal, fErr := strconv.ParseFloat(durationStr, 64)
			if fErr != nil {
				continue
			}
			duration = int64(math.Round(fVal))
		} else {
			continue
		}

		if duration <= 0 {
			continue
		}

		var elapsedSeconds int64
		if pod.Status.StartTime != nil {
			elapsedSeconds = int64(now.Sub(pod.Status.StartTime.Time).Seconds())
		} else if pod.Spec.NodeName != "" {
			elapsedSeconds = int64(now.Sub(pod.CreationTimestamp.Time).Seconds())
		} else {
			continue
		}
		remainingSeconds := duration - elapsedSeconds
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
		if remainingSeconds > maxRemainingTime {
			maxRemainingTime = remainingSeconds
		}
	}
	return maxRemainingTime
}

// REFINED: Simplified function signature to be more honest about its usage.
func (s *Chronos) CalculateOptimizedScore(nodeName string, maxRemainingTime int64, newPodDuration int64) int64 {
	const (
		binPackingPriority = 1000000
		extensionPriority  = 100000
		emptyNodePriority  = 1000
	)

	if maxRemainingTime > 0 {
		if newPodDuration <= maxRemainingTime {
			// PRIORITY 1: BIN-PACKING
			score := int64(binPackingPriority) + (maxRemainingTime * 100)
			klog.V(5).Infof("Score Node %s: BIN-PACKING -> %d", nodeName, score)
			return score
		}
		// PRIORITY 2: EXTENSION
		extensionPenalty := (newPodDuration - maxRemainingTime) * 100
		score := int64(extensionPriority) - extensionPenalty
		klog.V(5).Infof("Score Node %s: EXTENSION -> %d", nodeName, score)
		return score
	}

	// PRIORITY 3: EMPTY NODE
	klog.V(5).Infof("Score Node %s: EMPTY_NODE -> %d", nodeName, int64(emptyNodePriority))
	return int64(emptyNodePriority)
}

func (s *Chronos) ScoreExtensions() framework.ScoreExtensions {
	return s
}

func (s *Chronos) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	var minScore, maxScore int64 = math.MaxInt64, math.MinInt64
	for _, nodeScore := range scores {
		if nodeScore.Score < minScore {
			minScore = nodeScore.Score
		}
		if nodeScore.Score > maxScore {
			maxScore = nodeScore.Score
		}
	}
	if maxScore == minScore {
		for i := range scores {
			scores[i].Score = framework.MaxNodeScore
		}
		return nil
	}
	for i, nodeScore := range scores {
		scores[i].Score = (nodeScore.Score - minScore) * framework.MaxNodeScore / (maxScore - minScore)
	}
	return nil
}
