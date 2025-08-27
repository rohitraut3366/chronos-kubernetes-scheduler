package scheduler

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
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

// Chronos is a scheduler plugin that uses bin-packing logic with extension minimization
// to optimize resource utilization and minimize cluster commitment extensions.
// Now supports dual-mode operation: Individual (default) and Batch scheduling.
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

	batchModeEnabled := os.Getenv("CHRONOS_BATCH_MODE_ENABLED")
	testModeEnabled := os.Getenv("CHRONOS_TEST_MODE")

	if batchModeEnabled == "true" && testModeEnabled == "true" {
		chronos.batchModeEnabled = true
		klog.Infof("🧪 Chronos BATCH MODE enabled - Test mode (no actual scheduler)")
	} else if batchModeEnabled == "true" && h != nil {
		chronos.batchModeEnabled = true
		chronos.batchScheduler = NewBatchScheduler(h, chronos)
		chronos.batchScheduler.Start()
		klog.Infof("🚀 Chronos BATCH MODE enabled - High-throughput scheduling active")
	} else {
		chronos.batchModeEnabled = false
		klog.Infof("📝 Chronos INDIVIDUAL MODE enabled (default) - Traditional pod-by-pod scheduling")
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

	if s.batchModeEnabled {
		if _, inFlight := s.inFlightPods.Load(p.UID); inFlight {
			klog.V(4).Infof("Pod %s/%s is in-flight with batch scheduler, assigning minimal score.", p.Namespace, p.Name)
			return 1, framework.NewStatus(framework.Success)
		}

		klog.V(4).Infof("Pod %s/%s is a candidate for batch scheduler.", p.Namespace, p.Name)
		return 1, framework.NewStatus(framework.Success)
	}

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
	score := s.CalculateOptimizedScore(p, nodeInfo, maxRemainingTime, newPodDuration)

	klog.V(4).Infof("Pod: %s/%s, Node: %s, Existing: %ds, NewJob: %ds, Score: %d (individual mode)",
		p.Namespace, p.Name, nodeName, maxRemainingTime, newPodDuration, score)

	return score, framework.NewStatus(framework.Success)
}

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
		val, err := strconv.ParseInt(durationStr, 10, 64)
		if err == nil {
			duration = val
		} else {
			fVal, fErr := strconv.ParseFloat(durationStr, 64)
			if fErr != nil {
				continue
			}
			duration = int64(math.Round(fVal))
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

func (s *Chronos) CalculateBinPackingCompletionTime(maxRemainingTime, newPodDuration int64) int64 {
	if newPodDuration > maxRemainingTime {
		return newPodDuration
	}
	return maxRemainingTime
}

// CalculateOptimizedScore implements hierarchical bin-packing optimization:
// PRIORITY 1: Jobs that FIT within existing work windows (perfect bin-packing)
// PRIORITY 2: Jobs that EXTEND existing commitments (extension minimization)
// PRIORITY 3: Empty nodes (heavily penalized for cost optimization)
func (s *Chronos) CalculateOptimizedScore(p *v1.Pod, nodeInfo *framework.NodeInfo, maxRemainingTime int64, newPodDuration int64) int64 {
	nodeName := nodeInfo.Node().Name
	const (
		binPackingPriority = 1000000
		extensionPriority  = 100000
		emptyNodePriority  = 1000
	)

	var strategy string
	var score int64
	var extensionDuration int64
	var completionTime int64

	if maxRemainingTime > 0 {
		if newPodDuration <= maxRemainingTime {
			strategy = "BIN-PACKING"
			score = int64(binPackingPriority) + (maxRemainingTime * 100)
			completionTime = maxRemainingTime
			extensionDuration = 0
		} else {
			strategy = "EXTENSION"
			extensionPenalty := (newPodDuration - maxRemainingTime) * 100
			score = int64(extensionPriority) - extensionPenalty
			completionTime = newPodDuration
			extensionDuration = newPodDuration - maxRemainingTime
		}
	} else {
		strategy = "EMPTY-NODE"
		score = int64(emptyNodePriority)
		completionTime = newPodDuration
		extensionDuration = 0
	}

	klog.V(2).Infof(
		"CHRONOS_SCORE: Node=%s, Strategy=%s, NewPodDuration=%ds, ExistingWork=%ds, ExtensionDuration=%ds, CompletionTime=%ds, FinalScore=%d",
		nodeName,
		strategy,
		newPodDuration,
		maxRemainingTime,
		extensionDuration,
		completionTime,
		score,
	)

	return score
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
		normalized := (nodeScore.Score - minScore) * framework.MaxNodeScore / (maxScore - minScore)
		scores[i].Score = normalized
		klog.Infof("Pod: %s/%s, Node: %s, RawScore: %d, NormalizedScore: %d", pod.Namespace, pod.Name, nodeScore.Name, nodeScore.Score, normalized)
	}

	return nil
}
