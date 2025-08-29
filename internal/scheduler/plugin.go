package scheduler

import (
	"context"
	"fmt"
	"math"
	"os"
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
)

// Chronos is a scheduler plugin that uses bin-packing logic with extension minimization
// to optimize resource utilization and minimize cluster commitment extensions.
// Implements both QueueSort (for duration-based ordering) and Score (for node selection).
type Chronos struct {
	handle              framework.Handle
	reserveDelayEnabled bool
}

// Compile-time interface conformance checks
var _ framework.Plugin = &Chronos{}
var _ framework.QueueSortPlugin = &Chronos{}
var _ framework.ScorePlugin = &Chronos{}
var _ framework.ScoreExtensions = &Chronos{}
var _ framework.ReservePlugin = &Chronos{}

// New initializes a new plugin and returns it.
func New(ctx context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	// Cache environment variable configurations during initialization
	reserveDelayEnabled := os.Getenv("CHRONOS_RESERVE_DELAY") == "true"

	chronos := &Chronos{
		handle:              h,
		reserveDelayEnabled: reserveDelayEnabled,
	}

	// Check if queue sort mode is enabled
	queueSortEnabled := os.Getenv("CHRONOS_QUEUE_SORT_ENABLED")
	if queueSortEnabled == "true" {
		klog.Infof("🚀 Chronos Scheduler initialized with QueueSort + Score plugins")
	} else {
		klog.Infof("📝 Chronos Scheduler initialized with Score plugin only (default FIFO queue)")
	}

	// Log reserve delay configuration
	if reserveDelayEnabled {
		klog.Infof("⏳ Reserve delay enabled for sequential scheduling (testing mode)")
	}

	return chronos, nil
}

// Name returns the name of the plugin.
func (s *Chronos) Name() string {
	return PluginName
}

// Score is the core scheduling logic. It calculates a score for each node based on
// when the node is expected to become idle.
func (s *Chronos) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	klog.V(4).Infof("Scoring pod %s/%s for node %s", p.Namespace, p.Name, nodeName)

	// 1. Get the expected duration of the pod being scheduled.
	newPodDurationStr, ok := p.Annotations[JobDurationAnnotation]
	if !ok {
		// Pods without duration annotation get zero score in both modes
		return 0, framework.NewStatus(framework.Success)
	}

	// QueueSort orders pods by duration (longest first), Score selects best node

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

	// 3. calculates max remaining time by looking at annotations on all the pods on the node
	maxRemainingTime := s.calculateMaxRemainingTimeOptimized(nodeInfo.Pods)

	// 4. Apply pure time-based optimization strategy (NodeResourcesFit handles resource tie-breaking)
	score := s.CalculateOptimizedScore(p, nodeInfo, maxRemainingTime, newPodDuration)
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

		// Calculate elapsed time - handle both running and bound pods
		var elapsedSeconds int64

		if pod.Status.StartTime != nil {
			// Pod is running - use actual start time
			elapsedNanos := now.Sub(pod.Status.StartTime.Time).Nanoseconds()
			elapsedSeconds = elapsedNanos / 1e9
		} else if pod.Spec.NodeName != "" {
			// Pod is bound but not yet started - use creation time as baseline
			// Assume the pod "reserves" its expected duration from creation time
			elapsedNanos := now.Sub(pod.CreationTimestamp.Time).Nanoseconds()
			elapsedSeconds = elapsedNanos / 1e9
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
func (s *Chronos) CalculateOptimizedScore(p *v1.Pod, nodeInfo *framework.NodeInfo, maxRemainingTime int64, newPodDuration int64) int64 {
	// Pure time-based scoring - NodeResourcesFit plugin handles all resource tie-breaking
	// This keeps our plugin focused on its core competency: time-based bin-packing

	// Hierarchical scoring with clear priority levels
	const (
		binPackingPriority = 1000000 // Highest priority: jobs that fit within existing windows
		extensionPriority  = 100000  // Medium priority: jobs that extend commitments
		emptyNodePriority  = 1000    // Lowest priority: empty nodes
	)

	var finalScore int64
	var nodeStrategy string
	var completionTime string
	var extensionDuration int64

	if maxRemainingTime > 0 && newPodDuration <= maxRemainingTime {
		// PRIORITY 1: Job fits within existing work - PERFECT BIN-PACKING
		// This is true bin-packing: no extension of node commitment needed
		consolidationBonus := maxRemainingTime * 100 // Consolidation bonus - prefer longer existing work
		finalScore = int64(binPackingPriority) + consolidationBonus
		nodeStrategy = "BIN-PACKING"
		completionTime = fmt.Sprintf("%ds", maxRemainingTime)
		extensionDuration = 0

	} else if maxRemainingTime > 0 {
		// PRIORITY 2: Job extends beyond existing work - MINIMIZE EXTENSION
		// Choose node that minimizes the extension of cluster resource commitments
		extensionDuration = newPodDuration - maxRemainingTime
		extensionPenalty := -(extensionDuration * 100) // Extension penalty - heavy penalty for extending commitments
		finalScore = int64(extensionPriority) + extensionPenalty
		nodeStrategy = "EXTENSION"
		completionTime = fmt.Sprintf("%ds", newPodDuration)

	} else {
		// PRIORITY 3: Empty node - HEAVILY PENALIZED for cost optimization
		// Avoid empty nodes to enable Karpenter termination and reduce costs
		finalScore = int64(emptyNodePriority)
		nodeStrategy = "EMPTY-NODE"
		completionTime = fmt.Sprintf("%ds", newPodDuration)
		extensionDuration = newPodDuration
	}

	// Single consistent log format with essential scheduling details for easy parsing
	klog.Infof("CHRONOS_SCORE: Pod=%s/%s, Node=%s, Strategy=%s, NewPodDuration=%ds, maxRemainingTime=%ds, ExtensionDuration=%ds, CompletionTime=%s, FinalScore=%d",
		p.Namespace, p.Name, nodeInfo.Node().Name, nodeStrategy, newPodDuration, maxRemainingTime, extensionDuration, completionTime, finalScore)
	return finalScore
}

// Resource calculation removed - NodeResourcesFit plugin handles all resource-aware tie-breaking
// This keeps our plugin focused purely on time-based bin-packing optimization

// ScoreExtensions of the Score plugin.
func (s *Chronos) ScoreExtensions() framework.ScoreExtensions {
	return s
}

// Less is a function used by the QueueSort extension point to sort pods in the scheduling queue.
// It returns true if podInfo1 should be scheduled before podInfo2.
// We sort by duration (longest first) to implement Longest Processing Time (LPT) heuristic.
func (s *Chronos) Less(podInfo1, podInfo2 *framework.QueuedPodInfo) bool {
	klog.V(4).Infof("QueueSort: Comparing pods %s vs %s", podInfo1.Pod.Name, podInfo2.Pod.Name)

	// Priority 1: Respect existing pod priorities (higher priority first)
	var priority1, priority2 int32
	if podInfo1.Pod.Spec.Priority != nil {
		priority1 = *podInfo1.Pod.Spec.Priority
	}
	if podInfo2.Pod.Spec.Priority != nil {
		priority2 = *podInfo2.Pod.Spec.Priority
	}

	if priority1 != priority2 {
		result := priority1 > priority2
		klog.V(4).Infof("QueueSort: Priority decision - %s(p=%d) vs %s(p=%d) = %t",
			podInfo1.Pod.Name, priority1, podInfo2.Pod.Name, priority2, result)
		return result
	}

	// Priority 2: Within same priority class, sort by duration (longest first)
	duration1 := s.getPodDuration(podInfo1.Pod)
	duration2 := s.getPodDuration(podInfo2.Pod)

	if duration1 != duration2 {
		// Handle pods without duration annotations explicitly
		// Pods with -1 duration (no annotation) should be scheduled LAST
		if duration1 == -1 && duration2 >= 0 {
			klog.V(4).Infof("QueueSort: Duration decision - %s(no-annotation) vs %s(%ds) = false",
				podInfo1.Pod.Name, podInfo2.Pod.Name, duration2)
			return false // Pod2 first
		}
		if duration2 == -1 && duration1 >= 0 {
			klog.V(4).Infof("QueueSort: Duration decision - %s(%ds) vs %s(no-annotation) = true",
				podInfo1.Pod.Name, duration1, podInfo2.Pod.Name)
			return true // Pod1 first
		}
		// Both pods have valid durations - longest job first (LPT heuristic)
		result := duration1 > duration2
		klog.V(4).Infof("QueueSort: Duration decision - %s(%ds) vs %s(%ds) = %t (longest first)",
			podInfo1.Pod.Name, duration1, podInfo2.Pod.Name, duration2, result)
		return result
	}

	// Priority 3: If durations are equal, fall back to creation time (FIFO)
	result := podInfo1.Pod.CreationTimestamp.Before(&podInfo2.Pod.CreationTimestamp)
	klog.V(4).Infof("QueueSort: FIFO decision - %s vs %s = %t (earlier first)",
		podInfo1.Pod.Name, podInfo2.Pod.Name, result)
	return result
}

// getPodDuration extracts and parses the duration annotation from a pod.
// Returns -1 if annotation is missing or invalid, ensuring these pods are sorted last.
func (s *Chronos) getPodDuration(pod *v1.Pod) int64 {
	durationStr, exists := pod.Annotations[JobDurationAnnotation]
	if !exists {
		klog.V(4).Infof("getPodDuration: Pod %s has no duration annotation", pod.Name)
		return -1 // Pods without annotation are ranked last
	}

	durationFloat, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		klog.V(4).Infof("getPodDuration: Pod %s has invalid duration annotation: %s", pod.Name, durationStr)
		return -1 // Malformed annotations are ranked last
	}

	duration := int64(math.Round(durationFloat))
	klog.V(4).Infof("getPodDuration: Pod %s has duration %ds", pod.Name, duration)
	return duration
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
	}

	return nil
}

// ⚠️  TESTING ONLY - DO NOT USE IN PRODUCTION ⚠️
// Reserve implements the Reserve plugin interface to force add delay to scheduling.
// This ensures that QueueSort order is noticeable in scheduling order.
// Set CHRONOS_RESERVE_DELAY=true to enable artificial delays for testing.
func (s *Chronos) Reserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	// Use cached configuration instead of reading environment variable every time
	if !s.reserveDelayEnabled {
		return nil // Skip delay if not enabled
	}

	// Add artificial delay to force sequential scheduling
	// This ensures pods are scheduled in exact queue order
	delay := 2 * time.Second
	klog.V(4).Infof("Chronos Reserve: Adding %v delay for pod %s", delay, pod.Name)
	time.Sleep(delay)
	klog.V(4).Infof("Chronos Reserve: Delay completed for pod %s", pod.Name)
	return nil
}

// Unreserve implements the Unreserve plugin interface (required by ReservePlugin).
func (s *Chronos) Unreserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	// No action needed for unreserve in this implementation
	klog.V(4).Infof("🔄 Chronos Unreserve: Called for pod %s", pod.Name)
}
