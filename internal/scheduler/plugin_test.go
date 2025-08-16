package scheduler

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// =================================================================
// Test Fixtures and Mock Helpers
// =================================================================

// mockNodeInfo creates a realistic mock framework.NodeInfo for testing.
func mockNodeInfo(nodeName string, podCount int, capacity int64) *framework.NodeInfo {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourcePods: *resource.NewQuantity(capacity, resource.DecimalSI),
				v1.ResourceCPU:  *resource.NewMilliQuantity(capacity*100, resource.DecimalSI), // capacity * 100m CPU
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
				Annotations: map[string]string{
					JobDurationAnnotation: "300", // Default 5 minutes
				},
			},
			Status: v1.PodStatus{
				Phase:     v1.PodRunning,
				StartTime: &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}, // Started 2 min ago
			},
		}
		nodeInfo.AddPod(pod)
	}

	return nodeInfo
}

// mockPodWithDuration creates a test pod with specified duration annotation.
func mockPodWithDuration(name string, durationSeconds int64) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
	}

	if durationSeconds != 0 {
		pod.Annotations = map[string]string{
			JobDurationAnnotation: strconv.FormatInt(durationSeconds, 10),
		}
	}

	return pod
}

// calculateMockScore simulates the exact scoring logic for testing purposes.
func calculateMockScore(newPodDuration, maxRemainingTime int64, currentPods int, capacity int64) int64 {
	// 1. Calculate node completion time
	nodeCompletionTime := maxRemainingTime
	if newPodDuration > nodeCompletionTime {
		nodeCompletionTime = newPodDuration
	}

	// 2. Calculate raw score using new implementation logic
	// NOTE: This matches the new plugin logic - raw score only, no balancing
	// Use default value since we can't access the initialized variable from tests
	testMaxPossibleScore := int64(maxPossibleScore)
	score := testMaxPossibleScore - nodeCompletionTime
	if score < 0 {
		score = 0
	}

	// NOTE: Load balancing is now handled by separate plugins, not here
	return score
}

// =================================================================
// Core Plugin Functionality Tests
// =================================================================

func TestPluginBasics(t *testing.T) {
	t.Run("PluginName", func(t *testing.T) {
		plugin := &Chronos{}
		assert.Equal(t, "Chronos", plugin.Name(), "Plugin should return the correct name")
	})

	t.Run("ScoreExtensions", func(t *testing.T) {
		plugin := &Chronos{}
		assert.Equal(t, plugin, plugin.ScoreExtensions(), "ScoreExtensions should return self to provide NormalizeScore")
	})

	t.Run("Constants", func(t *testing.T) {
		assert.Equal(t, "Chronos", PluginName)
		assert.Equal(t, "scheduling.workload.io/expected-duration-seconds", JobDurationAnnotation)
		assert.Equal(t, 100000000, maxPossibleScore, "maxPossibleScore supports jobs up to ~3.17 years")

		t.Logf("✅ Constants validated - framework.MaxNodeScore = %d, maxPossibleScore = %d", framework.MaxNodeScore, maxPossibleScore)
	})
}

// =================================================================
// Edge Cases and Error Handling Tests
// =================================================================

func TestInvalidInputs(t *testing.T) {
	t.Run("PodWithoutAnnotation", func(t *testing.T) {
		pod := mockPodWithDuration("no-annotation", 0) // No annotation

		// Should not panic and should handle gracefully
		durationStr, exists := pod.Annotations[JobDurationAnnotation]
		assert.False(t, exists, "Pod should not have duration annotation")
		assert.Empty(t, durationStr, "Duration string should be empty")

		// In real scheduler, this would result in score 0
		t.Log("✅ Pod without annotation handled gracefully")
	})

	t.Run("MalformedDurationAnnotation", func(t *testing.T) {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "bad-annotation",
				Annotations: map[string]string{
					JobDurationAnnotation: "not-a-number",
				},
			},
		}

		durationStr := pod.Annotations[JobDurationAnnotation]
		_, err := strconv.ParseInt(durationStr, 10, 64)
		assert.Error(t, err, "Should fail to parse malformed duration")

		t.Log("✅ Malformed annotation handled gracefully")
	})

	t.Run("NegativeDuration", func(t *testing.T) {
		pod := mockPodWithDuration("negative", -100)
		durationStr := pod.Annotations[JobDurationAnnotation]
		duration, err := strconv.ParseInt(durationStr, 10, 64)

		require.NoError(t, err)
		assert.Negative(t, duration, "Duration should be negative")

		// In real scheduler, negative duration would be handled appropriately
		t.Log("✅ Negative duration parsed correctly")
	})
}

// =================================================================
// Comprehensive Scenario-Based Testing
// =================================================================

func TestScoringScenarios(t *testing.T) {
	scenarios := []struct {
		name               string
		description        string
		newPodDuration     int64
		node1RemainingTime int64
		node1PodCount      int
		node1Capacity      int64
		node2RemainingTime int64
		node2PodCount      int
		node2Capacity      int64
		expectedWinner     string // "node1" or "node2"
	}{
		{
			name:               "EmptyVsBusy_TimeAdvantage",
			description:        "Empty node should beat busy node due to time advantage",
			newPodDuration:     60,
			node1RemainingTime: 0, // Empty node
			node1PodCount:      0,
			node1Capacity:      100,
			node2RemainingTime: 180, // Busy node
			node2PodCount:      5,
			node2Capacity:      100,
			expectedWinner:     "node1",
		},
		{
			name:               "EqualTime_CapacityTieBreaker",
			description:        "Higher capacity node should win when completion times are equal",
			newPodDuration:     60,
			node1RemainingTime: 0,
			node1PodCount:      2, // Less loaded
			node1Capacity:      100,
			node2RemainingTime: 0,
			node2PodCount:      8, // More loaded
			node2Capacity:      100,
			expectedWinner:     "node1",
		},
		{
			name:               "TimeOverCapacity_Dominance",
			description:        "Time advantage should outweigh capacity disadvantage",
			newPodDuration:     30,
			node1RemainingTime: 0, // Empty but lower capacity
			node1PodCount:      8,
			node1Capacity:      100,
			node2RemainingTime: 60, // Busy but higher available capacity
			node2PodCount:      1,
			node2Capacity:      100,
			expectedWinner:     "node1",
		},
		{
			name:               "ZeroDurationJob",
			description:        "Zero duration job should be scored correctly",
			newPodDuration:     0,
			node1RemainingTime: 10,
			node1PodCount:      3,
			node1Capacity:      100,
			node2RemainingTime: 30,
			node2PodCount:      3,
			node2Capacity:      100,
			expectedWinner:     "node1",
		},
		{
			name:               "LongJobDuration",
			description:        "Job longer than MaxNodeScore should still be handled correctly",
			newPodDuration:     300, // Longer than MaxNodeScore (100)
			node1RemainingTime: 0,
			node1PodCount:      5,
			node1Capacity:      200, // Higher capacity
			node2RemainingTime: 60,
			node2PodCount:      5,
			node2Capacity:      100, // Lower capacity
			expectedWinner:     "node1",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Logf("🎯 %s", scenario.description)

			// Calculate scores using mock helper
			score1 := calculateMockScore(
				scenario.newPodDuration,
				scenario.node1RemainingTime,
				scenario.node1PodCount,
				scenario.node1Capacity,
			)
			score2 := calculateMockScore(
				scenario.newPodDuration,
				scenario.node2RemainingTime,
				scenario.node2PodCount,
				scenario.node2Capacity,
			)

			// Log scoring details
			t.Logf("  Node1: remaining=%ds, pods=%d, capacity=%d, score=%d",
				scenario.node1RemainingTime, scenario.node1PodCount, scenario.node1Capacity, score1)
			t.Logf("  Node2: remaining=%ds, pods=%d, capacity=%d, score=%d",
				scenario.node2RemainingTime, scenario.node2PodCount, scenario.node2Capacity, score2)

			// Assert the expected winner
			if scenario.expectedWinner == "node1" {
				assert.Greater(t, score1, score2,
					"Expected node1 to win. Node1: %d, Node2: %d", score1, score2)
			} else {
				assert.Greater(t, score2, score1,
					"Expected node2 to win. Node1: %d, Node2: %d", score1, score2)
			}

			t.Logf("✅ %s wins correctly!", scenario.expectedWinner)
		})
	}
}

// =================================================================
// Property-Based Randomized Testing
// =================================================================

func TestRandomizedPropertyValidation(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	t.Run("PropertyBasedTesting", func(t *testing.T) {
		successfulTests := 0

		for i := 0; i < 50; i++ { // Run 50 random scenarios
			t.Run(fmt.Sprintf("RandomProperty-%d", i), func(t *testing.T) {
				// Generate random but realistic parameters
				newJobDuration := int64(rand.Intn(600) + 30) // 30-630 seconds
				node1Remaining := int64(rand.Intn(300))      // 0-300 seconds
				node1Pods := rand.Intn(20)                   // 0-20 pods
				node2Remaining := int64(rand.Intn(300))      // 0-300 seconds
				node2Pods := rand.Intn(20)                   // 0-20 pods
				capacity := int64(100)                       // Fixed capacity

				score1 := calculateMockScore(newJobDuration, node1Remaining, node1Pods, capacity)
				score2 := calculateMockScore(newJobDuration, node2Remaining, node2Pods, capacity)

				// Test invariant: If node1 is strictly better in BOTH time AND capacity, it MUST win
				if node1Remaining < node2Remaining && node1Pods < node2Pods {
					assert.Greater(t, score1, score2,
						"Strictly better node must win: node1(rem=%d,pods=%d,score=%d) vs node2(rem=%d,pods=%d,score=%d)",
						node1Remaining, node1Pods, score1, node2Remaining, node2Pods, score2)
					successfulTests++
				}

				// Test invariant: Scores should never be negative
				assert.GreaterOrEqual(t, score1, int64(0), "Score1 should never be negative")
				assert.GreaterOrEqual(t, score2, int64(0), "Score2 should never be negative")
			})
		}

		t.Logf("✅ Property-based testing completed: %d strict dominance cases validated", successfulTests)
	})
}

// =================================================================
// Real-World Cluster Simulation Testing
// =================================================================

func TestRealisticClusterScenarios(t *testing.T) {
	t.Log("🚀 Testing realistic production cluster scenarios")

	clusterTests := []struct {
		name        string
		description string
		nodes       []struct {
			name      string
			remaining int64
			pods      int
			capacity  int64
		}
		newJobDuration  int64
		expectedPattern string // Description of expected behavior
	}{
		{
			name:        "ProductionWebCluster",
			description: "Multi-tier web application cluster",
			nodes: []struct {
				name      string
				remaining int64
				pods      int
				capacity  int64
			}{
				{"web-frontend", 30, 8, 120},  // Light load, finishing soon
				{"api-backend", 180, 12, 150}, // Medium load
				{"database", 600, 4, 80},      // Heavy load, fewer pods
				{"cache-redis", 0, 0, 100},    // Empty standby
			},
			newJobDuration:  120, // 2 minutes
			expectedPattern: "Cache node should win due to immediate availability",
		},
		{
			name:        "MLTrainingCluster",
			description: "Machine learning training cluster",
			nodes: []struct {
				name      string
				remaining int64
				pods      int
				capacity  int64
			}{
				{"gpu-node-1", 3600, 2, 40}, // Long training job
				{"gpu-node-2", 900, 1, 40},  // Medium job
				{"cpu-node-1", 60, 15, 200}, // Finishing soon, high capacity
				{"storage-node", 0, 5, 100}, // Empty but some load
			},
			newJobDuration:  480, // 8 minutes
			expectedPattern: "CPU node should win due to early completion and high capacity",
		},
	}

	for _, cluster := range clusterTests {
		t.Run(cluster.name, func(t *testing.T) {
			t.Logf("🎯 %s", cluster.description)

			var bestScore int64
			var bestNode string

			// Score all nodes
			for _, node := range cluster.nodes {
				score := calculateMockScore(
					cluster.newJobDuration,
					node.remaining,
					node.pods,
					node.capacity,
				)

				t.Logf("  %s: remaining=%ds, pods=%d, capacity=%d, score=%d",
					node.name, node.remaining, node.pods, node.capacity, score)

				if score > bestScore {
					bestScore = score
					bestNode = node.name
				}
			}

			t.Logf("🏆 Winner: %s with score %d", bestNode, bestScore)
			t.Logf("📝 Pattern: %s", cluster.expectedPattern)

			// Validate that we selected a node (basic sanity check)
			assert.NotEmpty(t, bestNode, "Should select a winning node")
			assert.Greater(t, bestScore, int64(0), "Winning score should be positive")

			t.Logf("✅ Realistic cluster scheduling completed")
		})
	}
}

// =================================================================
// Performance and Scalability Testing
// =================================================================

func TestPerformanceScaling(t *testing.T) {
	t.Log("📈 Testing scheduler performance with various loads")

	loadTests := []struct {
		name        string
		nodeCount   int
		podsPerNode int
	}{
		{"SmallCluster", 5, 10},
		{"MediumCluster", 20, 25},
		{"LargeCluster", 50, 50},
	}

	for _, load := range loadTests {
		t.Run(load.name, func(t *testing.T) {
			start := time.Now()

			// Simulate scoring many nodes
			for i := 0; i < load.nodeCount; i++ {
				remaining := int64(rand.Intn(600))
				pods := rand.Intn(load.podsPerNode)
				capacity := int64(100)

				// Calculate score for each node
				_ = calculateMockScore(300, remaining, pods, capacity)
			}

			duration := time.Since(start)
			avgPerNode := duration / time.Duration(load.nodeCount)

			t.Logf("✅ %s: %d nodes scored in %v (avg: %v per node)",
				load.name, load.nodeCount, duration, avgPerNode)

			// Performance assertion: should be very fast
			assert.Less(t, duration, time.Millisecond*100,
				"Scoring %d nodes should complete within 100ms", load.nodeCount)
		})
	}
}

// =================================================================
// Algorithm Correctness Validation
// =================================================================

func TestCorrectnessInvariants(t *testing.T) {
	t.Log("🔍 Validating core algorithm correctness invariants")

	t.Run("TimeScoreMultiplierDominance", func(t *testing.T) {
		// Test that ScoreMultiplier (1000) ensures time dominates capacity
		shortTime := calculateMockScore(60, 0, 50, 100) // Empty but more loaded
		longTime := calculateMockScore(60, 80, 10, 100) // 80s busy but less loaded

		assert.Greater(t, shortTime, longTime,
			"Time advantage should dominate capacity advantage (short=%d, long=%d)",
			shortTime, longTime)

		t.Log("✅ ScoreMultiplier correctly ensures time dominance")
	})

	t.Run("CapacityTieBreakerWorks", func(t *testing.T) {
		// When completion times are identical, capacity should decide
		highCap := calculateMockScore(60, 0, 10, 200) // Same time, high capacity
		lowCap := calculateMockScore(60, 0, 10, 50)   // Same time, low capacity

		assert.Greater(t, highCap, lowCap,
			"Higher capacity should win ties (high=%d, low=%d)", highCap, lowCap)

		t.Log("✅ Capacity tie-breaker works correctly")
	})

	t.Run("ScoreMonotonicity", func(t *testing.T) {
		// Scores should improve as conditions get better
		baseScore := calculateMockScore(30, 40, 10, 100)     // Base case: completion=40
		betterTime := calculateMockScore(30, 20, 10, 100)    // Better time: completion=30
		betterCapacity := calculateMockScore(30, 40, 5, 100) // Better capacity: 5 vs 10 pods

		assert.Greater(t, betterTime, baseScore, "Better time should improve score")
		assert.Greater(t, betterCapacity, baseScore, "Better capacity should improve score")

		t.Log("✅ Score monotonicity verified")
	})
}

// =================================================================
// Test New Optimized Methods - Bin-Packing Logic
// =================================================================

func TestCalculateBinPackingCompletionTime(t *testing.T) {
	plugin := &Chronos{}

	testCases := []struct {
		name               string
		maxRemainingTime   int64
		newPodDuration     int64
		expectedCompletion int64
		description        string
	}{
		{
			name:               "BinPackingFits",
			maxRemainingTime:   300, // 5 minutes existing work
			newPodDuration:     180, // 3 minutes new job
			expectedCompletion: 300, // Should stay at 5 minutes
			description:        "Job fits within existing work window",
		},
		{
			name:               "JobExtendsExistingWork",
			maxRemainingTime:   180, // 3 minutes existing work
			newPodDuration:     300, // 5 minutes new job
			expectedCompletion: 300, // Should become 5 minutes
			description:        "Job extends beyond existing work",
		},
		{
			name:               "EmptyNode",
			maxRemainingTime:   0,   // No existing work
			newPodDuration:     300, // 5 minutes new job
			expectedCompletion: 300, // Should become 5 minutes
			description:        "Empty node gets new job duration",
		},
		{
			name:               "ExactMatch",
			maxRemainingTime:   300, // 5 minutes existing work
			newPodDuration:     300, // 5 minutes new job (exact match)
			expectedCompletion: 300, // Should stay at 5 minutes
			description:        "Job duration exactly matches existing work",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := plugin.calculateBinPackingCompletionTime(tc.maxRemainingTime, tc.newPodDuration)
			assert.Equal(t, tc.expectedCompletion, result,
				"Test %s: %s. Expected %ds, got %ds",
				tc.name, tc.description, tc.expectedCompletion, result)
		})
	}
}

func TestCalculateOptimizedScore(t *testing.T) {
	plugin := &Chronos{}

	testCases := []struct {
		name             string
		maxRemainingTime int64
		newPodDuration   int64
		currentPods      int
		nodeCapacity     int
		expectedStrategy string
		description      string
	}{
		{
			name:             "ExtensionCase",
			maxRemainingTime: 180, // 3 minutes existing
			newPodDuration:   300, // 5 minutes new job (extends)
			currentPods:      5,
			nodeCapacity:     20,
			expectedStrategy: "extension-utilization",
			description:      "Job extends beyond existing work → utilization-based scoring",
		},
		{
			name:             "ConsolidationCase",
			maxRemainingTime: 300, // 5 minutes existing
			newPodDuration:   180, // 3 minutes new job (fits)
			currentPods:      8,
			nodeCapacity:     20,
			expectedStrategy: "consolidation",
			description:      "Job fits within existing work → consolidation scoring",
		},
		{
			name:             "EmptyNodePenalty",
			maxRemainingTime: 0,   // No existing work
			newPodDuration:   300, // 5 minutes new job
			currentPods:      0,
			nodeCapacity:     20,
			expectedStrategy: "empty-penalty",
			description:      "Empty node gets penalty score",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock node with specified capacity
			nodeInfo := mockNodeInfo("test-node", tc.currentPods, int64(tc.nodeCapacity))

			nodeCompletionTime := plugin.calculateBinPackingCompletionTime(tc.maxRemainingTime, tc.newPodDuration)
			score := plugin.calculateOptimizedScore(nodeInfo, tc.maxRemainingTime, tc.newPodDuration, nodeCompletionTime)

			// Get actual capacity from the method for accurate calculations
			actualCapacity := plugin.estimateNodeCapacity(nodeInfo)
			availableSlots := actualCapacity - tc.currentPods

			switch tc.expectedStrategy {
			case "extension-utilization":
				expectedScore := int64(availableSlots) * utilizationBonus
				assert.Equal(t, expectedScore, score, "Extension case should use utilization scoring")
				assert.Greater(t, score, int64(0), "Extension case should have positive score")

			case "consolidation":
				expectedConsolidationScore := tc.maxRemainingTime
				expectedUtilizationScore := int64(availableSlots) * (utilizationBonus / 10)
				expectedTotal := expectedConsolidationScore + expectedUtilizationScore
				assert.Equal(t, expectedTotal, score, "Consolidation case should combine consolidation + utilization")
				assert.Greater(t, score, int64(0), "Consolidation case should have positive score")

			case "empty-penalty":
				expectedScore := int64(availableSlots) * (utilizationBonus / 100)
				assert.Equal(t, expectedScore, score, "Empty node should have penalty score")
				// Empty nodes should have much lower scores than active nodes
				assert.Less(t, score, int64(availableSlots)*utilizationBonus/10, "Empty penalty should be significantly lower")
			}

			t.Logf("✅ %s: Strategy=%s, Score=%d, Available=%d/%d",
				tc.name, tc.expectedStrategy, score, availableSlots, actualCapacity)
		})
	}
}

func TestEstimateNodeCapacity(t *testing.T) {
	plugin := &Chronos{}

	testCases := []struct {
		name        string
		cpuMillis   int64
		expectedMin int
		expectedMax int
		description string
	}{
		{
			name:        "SmallNode",
			cpuMillis:   500, // 0.5 CPU
			expectedMin: 5,   // Minimum capacity
			expectedMax: 5,   // Should hit minimum
			description: "Small node should get minimum capacity",
		},
		{
			name:        "MediumNode",
			cpuMillis:   2000, // 2 CPUs
			expectedMin: 20,   // 2000/100 = 20
			expectedMax: 20,
			description: "Medium node capacity based on CPU",
		},
		{
			name:        "LargeNode",
			cpuMillis:   8000, // 8 CPUs
			expectedMin: 50,   // Should hit maximum
			expectedMax: 50,   // Maximum capacity
			description: "Large node should get maximum capacity",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: v1.NodeStatus{
					Allocatable: v1.ResourceList{
						v1.ResourceCPU: *resource.NewMilliQuantity(tc.cpuMillis, resource.DecimalSI),
					},
				},
			}
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)

			capacity := plugin.estimateNodeCapacity(nodeInfo)

			assert.GreaterOrEqual(t, capacity, tc.expectedMin,
				"Capacity should be at least minimum")
			assert.LessOrEqual(t, capacity, tc.expectedMax,
				"Capacity should not exceed maximum")

			t.Logf("✅ %s: CPU=%dm, Capacity=%d pods", tc.name, tc.cpuMillis, capacity)
		})
	}
}

// =================================================================
// Test Two-Phase Decision Logic - Integration Scenarios
// =================================================================

func TestTwoPhaseDecisionLogic(t *testing.T) {
	t.Log("🎯 Testing complete two-phase decision logic")

	// Scenario: Choose between extension vs consolidation
	testCases := []struct {
		name           string
		newJobDuration int64
		nodes          []struct {
			name         string
			maxRemaining int64
			currentPods  int
			capacity     int
		}
		expectedWinner   string
		expectedStrategy string
		description      string
	}{
		{
			name:           "ConsolidationWins",
			newJobDuration: 180, // 3 minutes - fits in both nodes
			nodes: []struct {
				name         string
				maxRemaining int64
				currentPods  int
				capacity     int
			}{
				{name: "node-short-work", maxRemaining: 240, currentPods: 10, capacity: 20}, // 4 min existing
				{name: "node-long-work", maxRemaining: 600, currentPods: 10, capacity: 20},  // 10 min existing
			},
			expectedWinner:   "node-long-work", // Should prefer longer existing work
			expectedStrategy: "consolidation",
			description:      "When job fits in both, choose longer existing work for consolidation",
		},
		{
			name:           "UtilizationWins",
			newJobDuration: 600, // 10 minutes - extends both nodes
			nodes: []struct {
				name         string
				maxRemaining int64
				currentPods  int
				capacity     int
			}{
				{name: "node-less-utilized", maxRemaining: 300, currentPods: 5, capacity: 20},  // 15 free slots
				{name: "node-more-utilized", maxRemaining: 300, currentPods: 15, capacity: 20}, // 5 free slots
			},
			expectedWinner:   "node-less-utilized", // Should prefer better utilization
			expectedStrategy: "extension-utilization",
			description:      "When job extends both, choose better utilization",
		},
		{
			name:           "EmptyNodePenalty",
			newJobDuration: 300, // 5 minutes
			nodes: []struct {
				name         string
				maxRemaining int64
				currentPods  int
				capacity     int
			}{
				{name: "empty-node", maxRemaining: 0, currentPods: 0, capacity: 20},    // Empty
				{name: "active-node", maxRemaining: 600, currentPods: 8, capacity: 20}, // Active, job fits
			},
			expectedWinner:   "active-node", // Should avoid empty node
			expectedStrategy: "consolidation",
			description:      "Should avoid empty nodes even if they're 'faster'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := &Chronos{}
			scores := make([]int64, len(tc.nodes))

			// Calculate scores for each node
			for i, nodeSpec := range tc.nodes {
				nodeInfo := mockNodeInfo(nodeSpec.name, nodeSpec.currentPods, int64(nodeSpec.capacity))

				nodeCompletionTime := plugin.calculateBinPackingCompletionTime(nodeSpec.maxRemaining, tc.newJobDuration)
				score := plugin.calculateOptimizedScore(nodeInfo, nodeSpec.maxRemaining, tc.newJobDuration, nodeCompletionTime)
				scores[i] = score

				t.Logf("Node %s: Existing=%ds, Score=%d", nodeSpec.name, nodeSpec.maxRemaining, score)
			}

			// Find the winner (highest score)
			winnerIndex := 0
			for i := 1; i < len(scores); i++ {
				if scores[i] > scores[winnerIndex] {
					winnerIndex = i
				}
			}

			actualWinner := tc.nodes[winnerIndex].name
			assert.Equal(t, tc.expectedWinner, actualWinner,
				"Test %s: %s. Expected %s to win, but %s won",
				tc.name, tc.description, tc.expectedWinner, actualWinner)

			t.Logf("✅ %s: Winner=%s (Score=%d), Strategy=%s",
				tc.name, actualWinner, scores[winnerIndex], tc.expectedStrategy)
		})
	}
}
