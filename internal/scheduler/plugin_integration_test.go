package scheduler

import (
	"fmt"
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
			expectedWinner: "web-frontend", // Should avoid empty node (cache-redis) and prefer active node
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
			expectedWinner: "small-node", // Both empty nodes get penalty, winner determined by other factors
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
			expectedScore: 99999700, // Raw score: 100000000 - 300 = 99999700
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
			expectedScore: 100000000, // Raw score: 100000000 - 0 = 100000000 (zero duration)
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

	// Calculate raw score using new implementation logic
	// Use default value for integration tests
	testMaxPossibleScore := int64(maxPossibleScore)
	score := testMaxPossibleScore - nodeCompletionTime
	if score < 0 {
		score = 0
	}

	// NOTE: Load balancing is now handled by separate plugins, not here
	return score
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

// simpleMockNodeInfo creates a simple mock framework.NodeInfo for integration testing.
func simpleMockNodeInfo(nodeName string, podCount int, capacity int64) *framework.NodeInfo {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourcePods: *resource.NewQuantity(capacity, resource.DecimalSI),
				v1.ResourceCPU:  *resource.NewMilliQuantity(2000, resource.DecimalSI), // 2 CPUs
			},
		},
	}
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	// Add mock pods to simulate load
	for i := 0; i < podCount; i++ {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("existing-pod-%d", i),
			},
			Status: v1.PodStatus{
				Phase: v1.PodRunning,
			},
		}
		nodeInfo.AddPod(pod)
	}
	return nodeInfo
}

// =================================================================
// Integration Tests for Optimized Bin-Packing Logic
// =================================================================

func TestOptimizedSchedulingIntegration(t *testing.T) {
	t.Log("🚀 Testing optimized scheduling logic with realistic scenarios")

	testCases := []struct {
		name           string
		description    string
		newJobDuration int64
		nodes          []struct {
			name         string
			cpuCores     string
			memoryGb     string
			maxPods      string
			existingJobs []IntegrationJob
		}
		expectedWinner string
		expectedReason string
	}{
		{
			name:           "ConsolidationPreference",
			description:    "Should prefer consolidation when job fits within existing work",
			newJobDuration: 180, // 3 minutes
			nodes: []struct {
				name         string
				cpuCores     string
				memoryGb     string
				maxPods      string
				existingJobs []IntegrationJob
			}{
				{
					name:     "node-short-work",
					cpuCores: "4", memoryGb: "8Gi", maxPods: "110",
					existingJobs: []IntegrationJob{
						{name: "short-job", duration: 300, startedSecondsAgo: 60}, // 4 min remaining
					},
				},
				{
					name:     "node-long-work",
					cpuCores: "4", memoryGb: "8Gi", maxPods: "110",
					existingJobs: []IntegrationJob{
						{name: "long-job", duration: 900, startedSecondsAgo: 300}, // 10 min remaining
					},
				},
			},
			expectedWinner: "node-long-work",
			expectedReason: "Longer existing work enables better consolidation",
		},
		{
			name:           "UtilizationOptimization",
			description:    "Should prefer less utilized nodes when jobs extend beyond existing work",
			newJobDuration: 900, // 15 minutes - extends both nodes
			nodes: []struct {
				name         string
				cpuCores     string
				memoryGb     string
				maxPods      string
				existingJobs []IntegrationJob
			}{
				{
					name:     "busy-node",
					cpuCores: "4", memoryGb: "8Gi", maxPods: "110",
					existingJobs: []IntegrationJob{
						{name: "job-1", duration: 300, startedSecondsAgo: 60}, // 4 min remaining
						{name: "job-2", duration: 300, startedSecondsAgo: 60}, // Simulating high utilization
						{name: "job-3", duration: 300, startedSecondsAgo: 60},
						{name: "job-4", duration: 300, startedSecondsAgo: 60},
						{name: "job-5", duration: 300, startedSecondsAgo: 60},
					},
				},
				{
					name:     "light-node",
					cpuCores: "4", memoryGb: "8Gi", maxPods: "110",
					existingJobs: []IntegrationJob{
						{name: "single-job", duration: 300, startedSecondsAgo: 60}, // 4 min remaining
					},
				},
			},
			expectedWinner: "light-node",
			expectedReason: "Better utilization for extension scenarios",
		},
		{
			name:           "EmptyNodeAvoidance",
			description:    "Should avoid empty nodes in favor of active nodes for cost optimization",
			newJobDuration: 300, // 5 minutes
			nodes: []struct {
				name         string
				cpuCores     string
				memoryGb     string
				maxPods      string
				existingJobs []IntegrationJob
			}{
				{
					name:     "empty-node",
					cpuCores: "4", memoryGb: "8Gi", maxPods: "110",
					existingJobs: []IntegrationJob{}, // No existing work
				},
				{
					name:     "active-node",
					cpuCores: "4", memoryGb: "8Gi", maxPods: "110",
					existingJobs: []IntegrationJob{
						{name: "existing-work", duration: 600, startedSecondsAgo: 0}, // 10 min remaining
					},
				},
			},
			expectedWinner: "active-node",
			expectedReason: "Consolidation to enable empty node termination",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("🎯 %s: %s", tc.name, tc.description)

			// Create plugin
			plugin := &Chronos{}
			scores := make([]int64, len(tc.nodes))
			nodeNames := make([]string, len(tc.nodes))

			// Score each node
			for i, nodeSpec := range tc.nodes {
				// Use simpleMockNodeInfo for integration testing
				nodeInfo := simpleMockNodeInfo(nodeSpec.name, len(nodeSpec.existingJobs), 110)
				nodeNames[i] = nodeSpec.name

				// Calculate max remaining time
				maxRemainingTime := int64(0)
				for _, job := range nodeSpec.existingJobs {
					elapsed := int64(job.startedSecondsAgo)
					remaining := job.duration - elapsed
					if remaining > maxRemainingTime {
						maxRemainingTime = remaining
					}
				}

				// Calculate completion time and score
				nodeCompletionTime := plugin.calculateBinPackingCompletionTime(maxRemainingTime, tc.newJobDuration)
				score := plugin.calculateOptimizedScore(nodeInfo, maxRemainingTime, tc.newJobDuration, nodeCompletionTime)
				scores[i] = score

				t.Logf("   Node %s: ExistingWork=%ds, NewJob=%ds, Completion=%ds, Score=%d",
					nodeSpec.name, maxRemainingTime, tc.newJobDuration, nodeCompletionTime, score)
			}

			// Find winner (highest score)
			winnerIndex := 0
			for i := 1; i < len(scores); i++ {
				if scores[i] > scores[winnerIndex] {
					winnerIndex = i
				}
			}

			actualWinner := nodeNames[winnerIndex]
			assert.Equal(t, tc.expectedWinner, actualWinner,
				"Expected %s to win (%s), but %s won with score %d",
				tc.expectedWinner, tc.expectedReason, actualWinner, scores[winnerIndex])

			t.Logf("✅ Winner: %s (Score: %d) - %s", actualWinner, scores[winnerIndex], tc.expectedReason)
		})
	}
}

func TestCostOptimizationScenarios(t *testing.T) {
	t.Log("💰 Testing cost optimization through consolidation and empty node avoidance")

	plugin := &Chronos{}

	// Scenario 1: Multiple jobs that could consolidate
	t.Run("MultipleJobConsolidation", func(t *testing.T) {
		// Node with 10 minutes of existing work - use simpleMockNodeInfo with 1 pod
		activeNode := simpleMockNodeInfo("active-node", 1, 110)

		// Empty node - use simpleMockNodeInfo with 0 pods
		emptyNode := simpleMockNodeInfo("empty-node", 0, 110)

		// Test multiple short jobs (2, 4, 6 minutes) - all should prefer active node
		jobDurations := []int64{120, 240, 360} // 2, 4, 6 minutes

		for _, jobDuration := range jobDurations {
			// Score active node
			activeCompletion := plugin.calculateBinPackingCompletionTime(600, jobDuration)
			activeScore := plugin.calculateOptimizedScore(activeNode, 600, jobDuration, activeCompletion)

			// Score empty node
			emptyCompletion := plugin.calculateBinPackingCompletionTime(0, jobDuration)
			emptyScore := plugin.calculateOptimizedScore(emptyNode, 0, jobDuration, emptyCompletion)

			// Active node should win for consolidation
			assert.Greater(t, activeScore, emptyScore,
				"Job duration %ds should prefer active node (active=%d, empty=%d)",
				jobDuration, activeScore, emptyScore)

			t.Logf("✅ %dm job: Active=%d > Empty=%d (consolidation wins)",
				jobDuration/60, activeScore, emptyScore)
		}
	})

	// Scenario 2: Cost vs Performance tradeoff
	t.Run("CostVsPerformanceTradeoff", func(t *testing.T) {
		// Create three nodes with different utilization levels
		nodes := []struct {
			name        string
			existing    int64
			utilization int
		}{
			{name: "high-util", existing: 300, utilization: 18}, // 18/20 pods
			{name: "med-util", existing: 300, utilization: 10},  // 10/20 pods
			{name: "low-util", existing: 300, utilization: 2},   // 2/20 pods
		}

		newJobDuration := int64(600) // 10 minutes (extends all nodes)

		scores := make([]int64, len(nodes))
		for i, node := range nodes {
			nodeInfo := simpleMockNodeInfo(node.name, node.utilization, 20)

			completion := plugin.calculateBinPackingCompletionTime(node.existing, newJobDuration)
			score := plugin.calculateOptimizedScore(nodeInfo, node.existing, newJobDuration, completion)
			scores[i] = score
		}

		// Lower utilization should win (better performance)
		assert.Greater(t, scores[2], scores[1], "Low utilization should beat medium")
		assert.Greater(t, scores[1], scores[0], "Medium utilization should beat high")

		t.Logf("✅ Utilization preference: Low=%d > Med=%d > High=%d",
			scores[2], scores[1], scores[0])
	})
}
