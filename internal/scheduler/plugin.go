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
	// maxPossibleScore - A large constant to invert the score. A lower completion time will result in a higher raw score.
	// Set to ~3.17 years (100,000,000 seconds ≈ 1,157 days) to provide excellent granularity for virtually all workloads.
	maxPossibleScore = 100000000
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
	klog.Infof("Scoring pod %s/%s for node %s", p.Namespace, p.Name, nodeName)

	// 1. Get the expected duration of the pod being scheduled.
	newPodDurationStr, ok := p.Annotations[JobDurationAnnotation]
	if !ok {
		klog.Infof("Pod %s/%s is missing annotation %s, skipping.", p.Namespace, p.Name, JobDurationAnnotation)
		return 0, framework.NewStatus(framework.Success)
	}
	newPodDuration, err := strconv.ParseInt(newPodDurationStr, 10, 64)
	if err != nil {
		klog.Warningf("Could not parse duration '%s' for pod %s/%s: %v", newPodDurationStr, p.Namespace, p.Name, err)
		return 0, framework.NewStatus(framework.Success)
	}

	// 2. Get node information.
	nodeInfo, err := s.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		klog.Errorf("Error getting node info for %s: %v", nodeName, err)
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q info: %s", nodeName, err))
	}

	// 3. Calculate the maximum remaining time for all pods currently on the node.
	maxRemainingTime := int64(0)
	now := time.Now()

	for _, existingPodInfo := range nodeInfo.Pods {
		existingPod := existingPodInfo.Pod
		if existingPod.Status.Phase == v1.PodSucceeded || existingPod.Status.Phase == v1.PodFailed {
			continue
		}
		durationStr, ok := existingPod.Annotations[JobDurationAnnotation]
		if !ok {
			continue
		}
		duration, err := strconv.ParseInt(durationStr, 10, 64)
		if err != nil {
			continue
		}
		if existingPod.Status.StartTime == nil {
			continue
		}
		startTime := existingPod.Status.StartTime.Time
		elapsedSeconds := now.Sub(startTime).Seconds()
		remainingSeconds := duration - int64(elapsedSeconds)
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
		if remainingSeconds > maxRemainingTime {
			maxRemainingTime = remainingSeconds
		}
	}

	// 4. Calculate completion time using bin-packing logic
	nodeCompletionTime := s.CalculateBinPackingCompletionTime(maxRemainingTime, newPodDuration)

	// 5. Apply two-phase optimization strategy
	score := s.CalculateOptimizedScore(nodeInfo, maxRemainingTime, newPodDuration, nodeCompletionTime)

	klog.Infof("Pod: %s/%s, Node: %s, Existing: %ds, NewJob: %ds, Completion: %ds, Score: %d (optimized)",
		p.Namespace, p.Name, nodeName, maxRemainingTime, newPodDuration, nodeCompletionTime, score)

	return score, framework.NewStatus(framework.Success)
}

// calculateBinPackingCompletionTime implements true bin-packing logic:
// If new job fits within existing work window, completion time doesn't change
func (s *Chronos) CalculateBinPackingCompletionTime(maxRemainingTime, newPodDuration int64) int64 {
	if newPodDuration <= maxRemainingTime {
		// Job fits within existing work window - completion time stays the same
		// This is the key insight: jobs can run concurrently within the window!
		return maxRemainingTime
	}

	// Job extends beyond existing work - completion time becomes the new job duration
	return newPodDuration
}

// calculateOptimizedScore implements hierarchical bin-packing optimization:
// PRIORITY 1: Jobs that FIT within existing work windows (perfect bin-packing)
// PRIORITY 2: Jobs that EXTEND existing commitments (extension minimization)
// PRIORITY 3: Empty nodes (heavily penalized for cost optimization)
func (s *Chronos) CalculateOptimizedScore(nodeInfo *framework.NodeInfo, maxRemainingTime int64, newPodDuration int64, nodeCompletionTime int64) int64 {
	// Get current pod count and capacity
	currentPods := len(nodeInfo.Pods)
	estimatedCapacity := s.estimateNodeCapacity(nodeInfo)
	availableSlots := estimatedCapacity - currentPods
	if availableSlots < 0 {
		availableSlots = 0
	}

	var finalScore int64
	var strategy string

	// Hierarchical scoring with clear priority levels
	const (
		binPackingPriority = 1000000 // Highest priority: jobs that fit within existing windows
		extensionPriority  = 100000  // Medium priority: jobs that extend commitments
		emptyNodePriority  = 1000    // Lowest priority: empty nodes
	)

	if maxRemainingTime > 0 && newPodDuration <= maxRemainingTime {
		// PRIORITY 1: Job fits within existing work - ALWAYS PREFERRED
		// This is true bin-packing: no extension of node commitment needed

		baseScore := int64(binPackingPriority)
		consolidationBonus := maxRemainingTime * 100   // Prefer longer existing work for better consolidation
		utilizationBonus := int64(availableSlots) * 10 // Tie-breaker: prefer nodes with more capacity

		finalScore = baseScore + consolidationBonus + utilizationBonus
		strategy = "bin-packing-fit"

		klog.V(4).Infof("Node %s: BIN-PACKING case - NewJob=%ds FITS in Existing=%ds, Base=%d, Consol=%d, Util=%d",
			nodeInfo.Node().Name, newPodDuration, maxRemainingTime, baseScore, consolidationBonus, utilizationBonus)

	} else if maxRemainingTime > 0 {
		// PRIORITY 2: Job extends beyond existing work
		// CRITICAL: Extension minimization takes priority over utilization

		baseScore := int64(extensionPriority)
		extensionPenalty := (newPodDuration - maxRemainingTime) * 100 // Strong penalty - extension minimization dominates
		utilizationBonus := int64(availableSlots) * 10                // Smaller bonus - only for tie-breaking

		finalScore = baseScore - extensionPenalty + utilizationBonus
		strategy = "extension-minimization"

		klog.V(4).Infof("Node %s: EXTENSION case - NewJob=%ds > Existing=%ds, Extension=%ds, Base=%d, Penalty=%d, Util=%d",
			nodeInfo.Node().Name, newPodDuration, maxRemainingTime, newPodDuration-maxRemainingTime, baseScore, extensionPenalty, utilizationBonus)

	} else {
		// PRIORITY 3: Empty node - lowest priority for cost optimization
		// Heavily penalized to encourage consolidation and enable node termination

		baseScore := int64(emptyNodePriority)
		utilizationBonus := int64(availableSlots) * 1 // Minimal utilization consideration

		finalScore = baseScore + utilizationBonus
		strategy = "empty-node-penalty"

		klog.V(4).Infof("Node %s: EMPTY node - heavily penalized, Base=%d, Util=%d",
			nodeInfo.Node().Name, baseScore, utilizationBonus)
	}

	klog.V(4).Infof("Node %s: Strategy=%s, Pods=%d/%d, Available=%d, Final=%d",
		nodeInfo.Node().Name, strategy, currentPods, estimatedCapacity, availableSlots, finalScore)

	return finalScore
}

// estimateNodeCapacity provides a rough estimate of how many pods a node can handle
// This could be made more sophisticated based on actual resource calculations
func (s *Chronos) estimateNodeCapacity(nodeInfo *framework.NodeInfo) int {
	node := nodeInfo.Node()

	// Simple heuristic based on node resources
	// In production, this could consider actual CPU/memory requirements
	allocatableCPU := node.Status.Allocatable.Cpu().MilliValue()

	// Assume average pod needs 100m CPU
	estimatedCapacity := int(allocatableCPU / 100)

	// Cap at reasonable limits
	if estimatedCapacity < 5 {
		estimatedCapacity = 5
	}
	if estimatedCapacity > 50 {
		estimatedCapacity = 50
	}

	return estimatedCapacity
}

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
