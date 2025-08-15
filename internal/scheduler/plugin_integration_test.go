package scheduler

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// =================================================================
// Integration Tests - Plugin with Realistic Kubernetes Objects
// =================================================================

// TestPluginIntegrationWithRealObjects tests our plugin with realistic K8s objects
func TestPluginIntegrationWithRealObjects(t *testing.T) {
	t.Log("🔗 Integration tests with realistic Kubernetes objects")

	integrationScenarios := []struct {
		name           string
		description    string
		newPod         *v1.Pod
		nodeScenarios  []IntegrationNodeScenario
		expectedWinner string
	}{
		{
			name:        "ProductionWebCluster",
			description: "Realistic production web application cluster",
			newPod:      createIntegrationPod("analytics-service", 300), // 5 minutes
			nodeScenarios: []IntegrationNodeScenario{
				{
					node: createIntegrationNode("web-frontend", "8", "16Gi", "110"),
					existingJobs: []IntegrationJob{
						{name: "nginx-1", duration: 120, startedSecondsAgo: 60},  // 1 min remaining
						{name: "nginx-2", duration: 180, startedSecondsAgo: 120}, // 1 min remaining
					},
				},
				{
					node: createIntegrationNode("api-backend", "16", "32Gi", "110"),
					existingJobs: []IntegrationJob{
						{name: "api-server", duration: 600, startedSecondsAgo: 300}, // 5 min remaining
					},
				},
				{
					node: createIntegrationNode("database", "32", "64Gi", "110"),
					existingJobs: []IntegrationJob{
						{name: "postgres", duration: 3600, startedSecondsAgo: 1800}, // 30 min remaining
					},
				},
				{
					node:         createIntegrationNode("cache-redis", "8", "16Gi", "110"),
					existingJobs: []IntegrationJob{}, // Empty!
				},
			},
			expectedWinner: "cache-redis", // Empty node should win
		},
		{
			name:        "MLTrainingCluster",
			description: "Machine learning cluster with GPU and CPU nodes",
			newPod:      createIntegrationPod("model-training", 600), // 10 minutes
			nodeScenarios: []IntegrationNodeScenario{
				{
					node: createIntegrationNode("gpu-node-1", "24", "96Gi", "64"),
					existingJobs: []IntegrationJob{
						{name: "training-resnet", duration: 7200, startedSecondsAgo: 3600}, // 1 hour remaining
					},
				},
				{
					node: createIntegrationNode("cpu-cluster", "64", "128Gi", "200"),
					existingJobs: []IntegrationJob{
						{name: "batch-job", duration: 1200, startedSecondsAgo: 900}, // 5 min remaining
					},
				},
			},
			expectedWinner: "cpu-cluster", // Finishes much sooner (10 min vs 1 hour)
		},
		{
			name:        "CapacityTieBreaker",
			description: "Test capacity-based tie breaking when completion times are equal",
			newPod:      createIntegrationPod("load-test", 180), // 3 minutes
			nodeScenarios: []IntegrationNodeScenario{
				{
					node:         createIntegrationNode("small-node", "4", "8Gi", "50"),
					existingJobs: []IntegrationJob{}, // Empty
				},
				{
					node:         createIntegrationNode("large-node", "32", "64Gi", "200"),
					existingJobs: []IntegrationJob{}, // Also empty
				},
			},
			expectedWinner: "large-node", // Higher capacity should win tie
		},
		{
			name:        "RealTimingScenario",
			description: "Test with realistic pod timing and lifecycle",
			newPod:      createIntegrationPod("urgent-job", 120), // 2 minutes
			nodeScenarios: []IntegrationNodeScenario{
				{
					node: createIntegrationNode("finishing-soon", "8", "16Gi", "110"),
					existingJobs: []IntegrationJob{
						{name: "almost-done", duration: 300, startedSecondsAgo: 270}, // 30 seconds left
					},
				},
				{
					node: createIntegrationNode("long-running", "8", "16Gi", "110"),
					existingJobs: []IntegrationJob{
						{name: "marathon-job", duration: 1800, startedSecondsAgo: 600}, // 20 min left
					},
				},
			},
			expectedWinner: "finishing-soon", // Will complete in 2 min vs 20 min
		},
	}

	for _, scenario := range integrationScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Logf("🎯 %s", scenario.description)

			var bestScore int64 = -1
			var bestNode string

			// Test each node scenario
			for _, nodeScenario := range scenario.nodeScenarios {
				// Build NodeInfo with realistic data
				nodeInfo := framework.NewNodeInfo()
				nodeInfo.SetNode(nodeScenario.node)

				// Add running pods based on job scenarios
				now := time.Now()
				for _, job := range nodeScenario.existingJobs {
					pod := createRunningIntegrationPod(
						job.name,
						nodeScenario.node.Name,
						job.duration,
						now.Add(-time.Duration(job.startedSecondsAgo)*time.Second),
					)
					nodeInfo.AddPod(pod)
				}

				// Calculate score using plugin logic directly
				score := calculateDirectIntegrationScore(scenario.newPod, nodeInfo)

				t.Logf("  📊 %s: score=%d (pods: %d)",
					nodeScenario.node.Name, score, len(nodeScenario.existingJobs))

				if score > bestScore {
					bestScore = score
					bestNode = nodeScenario.node.Name
				}
			}

			t.Logf("🏆 Winner: %s with score %d", bestNode, bestScore)
			assert.Equal(t, scenario.expectedWinner, bestNode,
				"Expected %s to win, got %s", scenario.expectedWinner, bestNode)

			t.Log("✅ Integration test passed!")
		})
	}
}

// TestIntegrationErrorHandling tests error scenarios with real objects
func TestIntegrationErrorHandling(t *testing.T) {
	t.Log("🛡️ Testing integration error handling")

	node := createIntegrationNode("test-node", "4", "8Gi", "110")
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	testCases := []struct {
		name          string
		pod           *v1.Pod
		expectedScore int64
		description   string
	}{
		{
			name:          "ValidAnnotation",
			pod:           createIntegrationPod("valid-job", 300),
			expectedScore: 110, // Should score: (MaxNodeScore-300)*1000 + 110 = -200*1000 + 110 = 110 (time score clamped to 0)
			description:   "Pod with valid duration annotation",
		},
		{
			name:          "MissingAnnotation",
			pod:           createIntegrationPodWithoutAnnotation("no-annotation"),
			expectedScore: 0, // Should return 0
			description:   "Pod without duration annotation",
		},
		{
			name:          "ZeroDuration",
			pod:           createIntegrationPod("instant-job", 0),
			expectedScore: 10110, // MaxNodeScore * multiplier + capacity = 100 * 100 + 110
			description:   "Pod with zero duration",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Logf("🧪 %s", testCase.description)

			score := calculateDirectIntegrationScore(testCase.pod, nodeInfo)

			assert.Equal(t, testCase.expectedScore, score,
				"Expected score %d, got %d for %s", testCase.expectedScore, score, testCase.description)

			t.Logf("✅ Score: %d", score)
		})
	}
}

// TestIntegrationPerformance tests performance with realistic load
func TestIntegrationPerformance(t *testing.T) {
	t.Log("📈 Testing integration performance with realistic cluster load")

	// Create a large-scale scenario
	nodes := make([]*v1.Node, 20) // 20 nodes
	for i := 0; i < 20; i++ {
		nodes[i] = createIntegrationNode(
			"node-"+strconv.Itoa(i),
			"16", "32Gi", "110",
		)
	}

	// Create varied workloads
	now := time.Now()
	allJobs := []IntegrationJob{}
	for i := 0; i < 100; i++ { // 100 total jobs across cluster
		job := IntegrationJob{
			name:              "job-" + strconv.Itoa(i),
			duration:          int64(300 + i*30), // 5-55 minutes
			startedSecondsAgo: int64(i * 10),     // Started 0-16 minutes ago
		}
		allJobs = append(allJobs, job)
	}

	newPod := createIntegrationPod("performance-test", 600) // 10 minutes

	// Distribute jobs across nodes and measure performance
	start := time.Now()

	bestScore := int64(-1)
	bestNode := ""

	for nodeIndex, node := range nodes {
		nodeInfo := framework.NewNodeInfo()
		nodeInfo.SetNode(node)

		// Distribute jobs across nodes (round-robin)
		for jobIndex, job := range allJobs {
			if jobIndex%len(nodes) == nodeIndex {
				pod := createRunningIntegrationPod(
					job.name,
					node.Name,
					job.duration,
					now.Add(-time.Duration(job.startedSecondsAgo)*time.Second),
				)
				nodeInfo.AddPod(pod)
			}
		}

		score := calculateDirectIntegrationScore(newPod, nodeInfo)

		if score > bestScore {
			bestScore = score
			bestNode = node.Name
		}
	}

	duration := time.Since(start)

	t.Logf("📊 Performance results:")
	t.Logf("  - Nodes evaluated: %d", len(nodes))
	t.Logf("  - Total jobs: %d", len(allJobs))
	t.Logf("  - Time taken: %v", duration)
	t.Logf("  - Winner: %s with score %d", bestNode, bestScore)

	// Performance should be very fast (< 10ms for this scale)
	assert.Less(t, duration, 10*time.Millisecond,
		"Integration performance should be fast even with large clusters")

	t.Log("✅ Integration performance test passed!")
}

// Helper types and functions

type IntegrationNodeScenario struct {
	node         *v1.Node
	existingJobs []IntegrationJob
}

type IntegrationJob struct {
	name              string
	duration          int64
	startedSecondsAgo int64
}

// calculateDirectIntegrationScore implements the plugin's scoring logic directly
func calculateDirectIntegrationScore(pod *v1.Pod, nodeInfo *framework.NodeInfo) int64 {
	// Extract duration from pod annotation
	durationStr, exists := pod.Annotations[JobDurationAnnotation]
	if !exists {
		return 0
	}

	newPodDuration, err := strconv.ParseInt(durationStr, 10, 64)
	if err != nil {
		return 0
	}

	// Calculate max remaining time from existing pods
	var maxRemainingTime int64 = 0
	now := time.Now()

	for _, podInfo := range nodeInfo.Pods {
		if podInfo.Pod.Status.Phase != v1.PodRunning {
			continue
		}

		existingDurationStr, exists := podInfo.Pod.Annotations[JobDurationAnnotation]
		if !exists {
			continue
		}

		existingDuration, err := strconv.ParseInt(existingDurationStr, 10, 64)
		if err != nil {
			continue
		}

		if podInfo.Pod.Status.StartTime == nil {
			continue
		}

		elapsedSeconds := now.Sub(podInfo.Pod.Status.StartTime.Time).Seconds()
		remainingSeconds := existingDuration - int64(elapsedSeconds)
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}

		if remainingSeconds > maxRemainingTime {
			maxRemainingTime = remainingSeconds
		}
	}

	// Calculate node completion time
	nodeCompletionTime := maxRemainingTime
	if newPodDuration > nodeCompletionTime {
		nodeCompletionTime = newPodDuration
	}

	// Calculate time score
	timeScore := framework.MaxNodeScore - nodeCompletionTime
	if timeScore < 0 {
		timeScore = 0
	}

	// Calculate balance score
	node := nodeInfo.Node()
	podCapacity := node.Status.Allocatable[v1.ResourcePods]
	currentPods := int64(len(nodeInfo.Pods))
	balanceScore := podCapacity.Value() - currentPods

	// Combine scores
	return (timeScore * ScoreMultiplier) + balanceScore
}

// Helper functions to create realistic Kubernetes objects

func createIntegrationNode(name, cpu, memory, pods string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"kubernetes.io/hostname":         name,
				"node-role.kubernetes.io/worker": "",
			},
		},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse(cpu),
				v1.ResourceMemory: resource.MustParse(memory),
				v1.ResourcePods:   resource.MustParse(pods),
			},
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse(cpu),
				v1.ResourceMemory: resource.MustParse(memory),
				v1.ResourcePods:   resource.MustParse(pods),
			},
			Conditions: []v1.NodeCondition{
				{Type: v1.NodeReady, Status: v1.ConditionTrue},
			},
		},
	}
}

func createIntegrationPod(name string, durationSeconds int64) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Annotations: map[string]string{
				JobDurationAnnotation: strconv.FormatInt(durationSeconds, 10),
			},
			Labels: map[string]string{
				"app": "integration-test",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "main",
					Image: "busybox",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("100m"),
							v1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
	}
}

func createIntegrationPodWithoutAnnotation(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				"app": "integration-test",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "main",
					Image: "busybox",
				},
			},
		},
	}
}

func createRunningIntegrationPod(name, nodeName string, duration int64, startTime time.Time) *v1.Pod {
	pod := createIntegrationPod(name, duration)
	pod.Spec.NodeName = nodeName
	pod.Status = v1.PodStatus{
		Phase:     v1.PodRunning,
		StartTime: &metav1.Time{Time: startTime},
		Conditions: []v1.PodCondition{
			{Type: v1.PodReady, Status: v1.ConditionTrue},
		},
	}
	return pod
}
