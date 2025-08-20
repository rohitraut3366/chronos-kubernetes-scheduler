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
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	// PluginName is the name of the custom scheduler plugin.
	PluginName = "Chronos"
	// JobDurationAnnotation is the annotation on a pod that specifies its expected runtime in seconds.
	JobDurationAnnotation = "scheduling.workload.io/expected-duration-seconds"
	// DefaultStartupDelaySeconds is the estimated time for containers to reach Running state
	// This should be tuned based on your environment (e.g., image pull times, resource contention)
	DefaultStartupDelaySeconds = 240 // 4 minutes
)

// Chronos is a scheduler plugin that uses bin-packing logic with extension minimization
// to optimize resource utilization and minimize cluster commitment extensions.
type Chronos struct {
	handle framework.Handle
}

// New initializes a new plugin and returns it.
func New(ctx context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	klog.Infof("Initializing Chronos plugin with bin-packing + extension minimization + utilization optimization")
	return &Chronos{
		handle: h,
	}, nil
}

// Name returns the name of the plugin.
func (s *Chronos) Name() string {
	return PluginName
}

// Score is the core scheduling logic. It calculates a score for each node based on
// when the node is expected to become idle.
func (s *Chronos) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	// Only log at debug level to reduce hot path overhead
	klog.V(4).Infof("Scoring pod %s/%s for node %s", p.Namespace, p.Name, nodeName)

	// 1. Get the expected duration of the pod being scheduled.
	newPodDurationStr, ok := p.Annotations[JobDurationAnnotation]
	if !ok {
		klog.Infof("Pod %s/%s is missing annotation %s, skipping.", p.Namespace, p.Name, JobDurationAnnotation)
		return 0, framework.NewStatus(framework.Success)
	}
	// Parse duration - support both integer and decimal values
	newPodDurationFloat, err := strconv.ParseFloat(newPodDurationStr, 64)
	if err != nil {
		klog.Warningf("Could not parse duration '%s' for pod %s/%s: %v", newPodDurationStr, p.Namespace, p.Name, err)
		return 0, framework.NewStatus(framework.Success)
	}
	newPodDuration := int64(math.Round(newPodDurationFloat)) // Use math.Round for predictable conversion (e.g., 600.75 becomes 601)

	// 2. Get node information.
	nodeInfo, err := s.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		klog.Errorf("Error getting node info for %s: %v", nodeName, err)
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q info: %s", nodeName, err))
	}

	// 3. Calculate node state using optimized single-pass algorithm
	maxRemainingTime := s.calculateMaxRemainingTimeOptimized(nodeInfo.Pods)

	// 4. Calculate completion time using bin-packing logic
	nodeCompletionTime := s.CalculateBinPackingCompletionTime(maxRemainingTime, newPodDuration)

	// 5. Apply pure time-based optimization strategy (NodeResourcesFit handles resource tie-breaking)
	score := s.CalculateOptimizedScore(nodeInfo, maxRemainingTime, newPodDuration, nodeCompletionTime)

	// Only log scoring results at debug level to improve performance
	klog.V(4).Infof("Pod: %s/%s, Node: %s, Existing: %ds, NewJob: %ds, Completion: %ds, Score: %d (optimized)",
		p.Namespace, p.Name, nodeName, maxRemainingTime, newPodDuration, nodeCompletionTime, score)

	return score, framework.NewStatus(framework.Success)
}

// calculateMaxRemainingTimeOptimized - Performance-optimized remaining time calculation
// Single pass through pods with early termination and minimal allocations
func (s *Chronos) calculateMaxRemainingTimeOptimized(pods []*framework.PodInfo) int64 {
	if len(pods) == 0 {
		return 0
	}

	maxRemainingTime := int64(0)
	now := time.Now()

	// Single optimized loop - no redundant operations
	for _, podInfo := range pods {
		pod := podInfo.Pod

		// Fast early termination for completed pods
		switch pod.Status.Phase {
		case v1.PodSucceeded, v1.PodFailed:
			continue
		}

		// Fast annotation lookup with existence check
		durationStr, exists := pod.Annotations[JobDurationAnnotation]
		if !exists {
			continue
		}

		// Parse duration - support both integer and decimal values
		durationFloat, err := strconv.ParseFloat(durationStr, 64)
		if err != nil {
			continue
		}
		duration := int64(math.Round(durationFloat)) // Use math.Round for predictable conversion
		if duration <= 0 {
			continue
		}

		// Calculate elapsed time - handle both running and bound-but-pending pods
		var elapsedSeconds int64

		if pod.Status.StartTime != nil {
			// Pod is running - use actual start time
			elapsedNanos := now.Sub(pod.Status.StartTime.Time).Nanoseconds()
			elapsedSeconds = elapsedNanos / 1e9
		} else if pod.Spec.NodeName != "" {
			// Pod is bound but not yet started - estimate based on binding time
			// Use creation time + estimated container startup delay
			estimatedStartTime := pod.CreationTimestamp.Add(DefaultStartupDelaySeconds * time.Second)
			if now.After(estimatedStartTime) {
				elapsedNanos := now.Sub(estimatedStartTime).Nanoseconds()
				elapsedSeconds = elapsedNanos / 1e9
			} else {
				elapsedSeconds = 0 // Pod hasn't "started" yet by our estimate
			}
		} else {
			// Pod is not bound - skip
			continue
		}
		remainingSeconds := duration - elapsedSeconds

		// Clamp negative values and update maximum
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
		if remainingSeconds > maxRemainingTime {
			maxRemainingTime = remainingSeconds
		}
	}

	return maxRemainingTime
}

// CalculateBinPackingCompletionTime determines the completion time for bin-packing logic.
// The completion time is simply the maximum of the existing window and the new job's duration.
// This concisely represents both the "fit" (concurrent execution) and "extend" scenarios.
func (s *Chronos) CalculateBinPackingCompletionTime(maxRemainingTime, newPodDuration int64) int64 {
	if newPodDuration > maxRemainingTime {
		return newPodDuration
	}
	return maxRemainingTime
}

// calculateOptimizedScore implements hierarchical bin-packing optimization:
// PRIORITY 1: Jobs that FIT within existing work windows (perfect bin-packing)
// PRIORITY 2: Jobs that EXTEND existing commitments (extension minimization)
// PRIORITY 3: Empty nodes (heavily penalized for cost optimization)
func (s *Chronos) CalculateOptimizedScore(nodeInfo *framework.NodeInfo, maxRemainingTime int64, newPodDuration int64, nodeCompletionTime int64) int64 {
	// Pure time-based scoring - NodeResourcesFit plugin handles all resource tie-breaking
	// This keeps our plugin focused on its core competency: time-based bin-packing

	// Hierarchical scoring with clear priority levels
	const (
		binPackingPriority = 1000000 // Highest priority: jobs that fit within existing windows
		extensionPriority  = 100000  // Medium priority: jobs that extend commitments
		emptyNodePriority  = 1000    // Lowest priority: empty nodes
	)

	if maxRemainingTime > 0 && newPodDuration <= maxRemainingTime {
		// PRIORITY 1: Job fits within existing work - PERFECT BIN-PACKING
		// This is true bin-packing: no extension of node commitment needed
		baseScore := int64(binPackingPriority)
		consolidationBonus := maxRemainingTime * 100 // Prefer longer existing work for better consolidation

		finalScore := baseScore + consolidationBonus

		klog.V(4).Infof("Node %s: BIN-PACKING - NewJob=%ds fits in Existing=%ds, Base=%d, Bonus=%d, Final=%d",
			nodeInfo.Node().Name, newPodDuration, maxRemainingTime, baseScore, consolidationBonus, finalScore)
		return finalScore

	} else if maxRemainingTime > 0 {
		// PRIORITY 2: Job extends beyond existing work - MINIMIZE EXTENSION
		// Choose node that minimizes the extension of cluster resource commitments
		baseScore := int64(extensionPriority)
		extensionPenalty := (newPodDuration - maxRemainingTime) * 100 // Heavy penalty for extending commitments

		finalScore := baseScore - extensionPenalty

		klog.V(4).Infof("Node %s: EXTENSION - NewJob=%ds > Existing=%ds, Extension=%ds, Base=%d, Penalty=%d, Final=%d",
			nodeInfo.Node().Name, newPodDuration, maxRemainingTime, newPodDuration-maxRemainingTime,
			baseScore, extensionPenalty, finalScore)
		return finalScore

	} else {
		// PRIORITY 3: Empty node - HEAVILY PENALIZED for cost optimization
		// Avoid empty nodes to enable Karpenter termination and reduce costs
		baseScore := int64(emptyNodePriority)

		klog.V(4).Infof("Node %s: EMPTY NODE - Base=%d, Final=%d (penalized for cost optimization)",
			nodeInfo.Node().Name, baseScore, baseScore)
		return baseScore
	}
}

// Resource calculation removed - NodeResourcesFit plugin handles all resource-aware tie-breaking
// This keeps our plugin focused purely on time-based bin-packing optimization

// ScoreExtensions of the Score plugin.
func (s *Chronos) ScoreExtensions() framework.ScoreExtensions {
	return s
}

// NormalizeScore is the key to making this work for jobs of any duration.
// It takes the raw scores from the Score function and scales them to a 0-100 range.
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

	// If all scores are the same, set them all to maximum score.
	if maxScore == minScore {
		for i := range scores {
			scores[i].Score = framework.MaxNodeScore // Give all nodes a perfect score
		}
		return nil
	}

	for i, nodeScore := range scores {
		// Formula to scale a value from one range [min, max] to another [0, 100].
		normalized := (nodeScore.Score - minScore) * framework.MaxNodeScore / (maxScore - minScore)
		scores[i].Score = normalized
		klog.Infof("Pod: %s/%s, Node: %s, RawScore: %d, NormalizedScore: %d", pod.Namespace, pod.Name, nodeScore.Name, nodeScore.Score, normalized)
	}

	return nil
}
