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
	// Only log at debug level to reduce hot path overhead
	klog.V(4).Infof("Scoring pod %s/%s for node %s", p.Namespace, p.Name, nodeName)

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

	// 3. Calculate node state using optimized single-pass algorithm
	maxRemainingTime := s.calculateMaxRemainingTimeOptimized(nodeInfo.Pods)

	// 4. Calculate completion time using bin-packing logic
	nodeCompletionTime := s.CalculateBinPackingCompletionTime(maxRemainingTime, newPodDuration)

	// 5. Apply two-phase optimization strategy
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

		// Fast integer parsing (this is already quite optimized in Go)
		duration, err := strconv.ParseInt(durationStr, 10, 64)
		if err != nil || duration <= 0 {
			continue
		}

		// Fast nil check and time calculation
		if pod.Status.StartTime == nil {
			continue
		}

		// Optimized time calculation - avoid float operations when possible
		elapsedNanos := now.Sub(pod.Status.StartTime.Time).Nanoseconds()
		elapsedSeconds := elapsedNanos / 1e9
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
	// Get accurate resource utilization (like default scheduler)
	resourceUtil := s.calculateResourceUtilization(nodeInfo)

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
		consolidationBonus := maxRemainingTime * 100            // Prefer longer existing work for better consolidation
		resourceBonus := int64(resourceUtil.ResourceScore) * 10 // Tie-breaker: prefer nodes with more available resources

		finalScore = baseScore + consolidationBonus + resourceBonus
		strategy = "bin-packing-fit"

		klog.V(4).Infof("Node %s: BIN-PACKING case - NewJob=%ds FITS in Existing=%ds, Base=%d, Consol=%d, Resource=%d",
			nodeInfo.Node().Name, newPodDuration, maxRemainingTime, baseScore, consolidationBonus, resourceBonus)

	} else if maxRemainingTime > 0 {
		// PRIORITY 2: Job extends beyond existing work
		// CRITICAL: Extension minimization takes priority over utilization

		baseScore := int64(extensionPriority)
		extensionPenalty := (newPodDuration - maxRemainingTime) * 100 // Strong penalty - extension minimization dominates
		resourceBonus := int64(resourceUtil.ResourceScore) * 5        // Smaller bonus - only for tie-breaking

		finalScore = baseScore - extensionPenalty + resourceBonus
		strategy = "extension-minimization"

		klog.V(4).Infof("Node %s: EXTENSION case - NewJob=%ds > Existing=%ds, Extension=%ds, Base=%d, Penalty=%d, Resource=%d",
			nodeInfo.Node().Name, newPodDuration, maxRemainingTime, newPodDuration-maxRemainingTime, baseScore, extensionPenalty, resourceBonus)

	} else {
		// PRIORITY 3: Empty node - lowest priority for cost optimization
		// Heavily penalized to encourage consolidation and enable node termination

		baseScore := int64(emptyNodePriority)
		resourceBonus := int64(resourceUtil.ResourceScore) * 1 // Minimal resource consideration

		finalScore = baseScore + resourceBonus
		strategy = "empty-node-penalty"

		klog.V(4).Infof("Node %s: EMPTY node - heavily penalized, Base=%d, Resource=%d",
			nodeInfo.Node().Name, baseScore, resourceBonus)
	}

	klog.V(4).Infof("Node %s: Strategy=%s, CPU=%.1f%%, Memory=%.1f%%, ResourceScore=%d, Final=%d",
		nodeInfo.Node().Name, strategy, resourceUtil.CPUUtilizationPercent*100,
		resourceUtil.MemoryUtilizationPercent*100, resourceUtil.ResourceScore, finalScore)

	return finalScore
}

// ResourceUtilization represents the current resource usage on a node
type ResourceUtilization struct {
	CPUUtilizationPercent    float64 // 0.0 to 1.0
	MemoryUtilizationPercent float64 // 0.0 to 1.0
	AvailableCPUMillis       int64
	AvailableMemoryBytes     int64
	ResourceScore            int // 0-100, higher = more available resources
}

func (s *Chronos) calculateResourceUtilization(nodeInfo *framework.NodeInfo) ResourceUtilization {
	node := nodeInfo.Node()

	// Get total allocatable resources (same as default scheduler)
	allocatableCPU := node.Status.Allocatable.Cpu().MilliValue()
	allocatableMemory := node.Status.Allocatable.Memory().Value()

	// Calculate currently used resources by summing all pod requests (optimized single-pass)
	var usedCPU, usedMemory int64
	for _, podInfo := range nodeInfo.Pods {
		pod := podInfo.Pod

		// Fast early termination for completed pods
		switch pod.Status.Phase {
		case v1.PodSucceeded, v1.PodFailed:
			continue
		}

		// Optimized resource accumulation - minimize allocations
		for _, container := range pod.Spec.Containers {
			requests := container.Resources.Requests
			if requests != nil {
				if cpu := requests.Cpu(); !cpu.IsZero() {
					usedCPU += cpu.MilliValue()
				}
				if memory := requests.Memory(); !memory.IsZero() {
					usedMemory += memory.Value()
				}
			}
		}
	}

	// Calculate utilization percentages (same as Kubernetes)
	cpuUtilization := 0.0
	memoryUtilization := 0.0
	if allocatableCPU > 0 {
		cpuUtilization = float64(usedCPU) / float64(allocatableCPU)
	}
	if allocatableMemory > 0 {
		memoryUtilization = float64(usedMemory) / float64(allocatableMemory)
	}

	// Available resources
	availableCPU := allocatableCPU - usedCPU
	availableMemory := allocatableMemory - usedMemory
	if availableCPU < 0 {
		availableCPU = 0
	}
	if availableMemory < 0 {
		availableMemory = 0
	}

	// Calculate resource score (higher = better)
	// Use the limiting resource (CPU or Memory) - same principle as default scheduler
	limitingUtilization := cpuUtilization
	if memoryUtilization > cpuUtilization {
		limitingUtilization = memoryUtilization
	}

	// Score: 100 = empty node, 0 = fully utilized node
	resourceScore := int((1.0 - limitingUtilization) * 100)
	if resourceScore < 0 {
		resourceScore = 0
	}
	if resourceScore > 100 {
		resourceScore = 100
	}

	klog.V(4).Infof("Node %s: CPU=%dm/%dm (%.1f%%), Memory=%d/%d (%.1f%%), Score=%d",
		node.Name, usedCPU, allocatableCPU, cpuUtilization*100,
		usedMemory, allocatableMemory, memoryUtilization*100, resourceScore)

	return ResourceUtilization{
		CPUUtilizationPercent:    cpuUtilization,
		MemoryUtilizationPercent: memoryUtilization,
		AvailableCPUMillis:       availableCPU,
		AvailableMemoryBytes:     availableMemory,
		ResourceScore:            resourceScore,
	}
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
