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

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	// PluginName is the name of the custom scheduler plugin.
	PluginName = "FastestEmptyNode"
	// JobDurationAnnotation is the annotation on a pod that specifies its expected runtime in seconds.
	JobDurationAnnotation = "job-duration.example.com/seconds"
	// ScoreMultiplier is used to ensure the primary scoring factor (time) outweighs the tie-breaker (pod count).
	ScoreMultiplier = 1000
)

// FastestEmptyNode is a scheduler plugin that schedules pods on the node
// that is predicted to be empty the soonest. It uses pod count as a tie-breaker.
type FastestEmptyNode struct {
	handle framework.Handle
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	klog.Infof("Initializing FastestEmptyNode plugin")
	return &FastestEmptyNode{
		handle: h,
	}, nil
}

// Name returns the name of the plugin.
func (s *FastestEmptyNode) Name() string {
	return PluginName
}

// Score is the core scheduling logic. It calculates a score for each node based on
// when the node is expected to become idle.
func (s *FastestEmptyNode) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
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

	// 4. Determine when the node will be free after scheduling the new pod.
	nodeCompletionTime := maxRemainingTime
	if newPodDuration > nodeCompletionTime {
		nodeCompletionTime = newPodDuration
	}

	// 5. Calculate the primary score based on completion time.
	timeScore := framework.MaxNodeScore - nodeCompletionTime
	if timeScore < 0 {
		timeScore = 0
	}

	// 6. *** NEW: BALANCING TIE-BREAKER ***
	// Calculate a secondary score based on the number of running pods.
	// A node with fewer pods gets a higher score.
	podCapacity := nodeInfo.Node().Status.Allocatable.Pods().Value()
	currentPodCount := int64(len(nodeInfo.Pods))
	balanceScore := podCapacity - currentPodCount
	if balanceScore < 0 {
		balanceScore = 0
	}

	// 7. Combine the scores.
	// The timeScore is multiplied by a large number to ensure it always takes precedence.
	// The balanceScore will only influence the result if the timeScores are identical.
	finalScore := (timeScore * ScoreMultiplier) + balanceScore

	klog.Infof("Pod: %s/%s, Node: %s, CompletionTime: %d, TimeScore: %d, PodCount: %d, BalanceScore: %d, FinalScore: %d",
		p.Namespace, p.Name, nodeName, nodeCompletionTime, timeScore, currentPodCount, balanceScore, finalScore)

	return finalScore, framework.NewStatus(framework.Success)
}

// ScoreExtensions of the Score plugin.
func (s *FastestEmptyNode) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// main is the entry point for the scheduler.
func main() {
	command := app.NewSchedulerCommand(
		app.WithPlugin(PluginName, New),
	)

	if err := command.Execute(); err != nil {
		klog.Fatalf("Error executing scheduler command: %v", err)
		os.Exit(1)
	}
}
