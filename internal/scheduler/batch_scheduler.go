package scheduler

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

var (
	batchRunCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chronos_batch_runs_total",
		Help: "The total number of batch scheduling runs.",
	})
	podsScheduledCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronos_batch_pods_scheduled_total",
		Help: "The total number of pods scheduled by the batch scheduler.",
	}, []string{"status"})
	batchRunDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "chronos_batch_run_duration_seconds",
		Help:    "The duration of batch scheduling runs.",
		Buckets: prometheus.DefBuckets,
	})
	nodeSelectorsValidated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronos_node_selectors_validated_total",
		Help: "Number of pods processed with nodeSelector validation.",
	}, []string{"status"})

	// OPTIMIZATION: New metrics for resource caching
	resourceCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chronos_resource_cache_hits_total",
		Help: "Number of resource cache hits",
	})
	resourceCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chronos_resource_cache_misses_total",
		Help: "Number of resource cache misses",
	})
	resourceCacheSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "chronos_resource_cache_size",
		Help: "Current size of the resource cache",
	})
	resourceCacheEvictions = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chronos_resource_cache_evictions_total",
		Help: "Number of resource cache evictions due to size limits",
	})

	// RESILIENCE: Simple cache reliability metrics
	cacheReliabilityCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronos_cache_reliability_total",
		Help: "Cache reliability events (hits, misses, fallbacks).",
	}, []string{"event_type"}) // "cache_hit", "cache_miss", "api_fallback_success", "api_fallback_failed"

	// COOLDOWN: Metrics for failed pod cooldown system
	cooldownResetCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chronos_cooldown_resets_total",
		Help: "Number of failed pods reset after cooldown period",
	})
	cooldownActiveGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "chronos_pods_in_cooldown",
		Help: "Number of pods currently in cooldown period",
	})

	// BIND: Enhanced binding metrics
	chronosBindSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronos_scheduler_bind_success_total",
		Help: "Number of successful pod binds",
	}, []string{"node"})

	chronosBindFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronos_scheduler_bind_failures_total",
		Help: "Number of failed pod binds by reason",
	}, []string{"reason"})

	chronosBindDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "chronos_scheduler_bind_duration_seconds",
		Help:    "Latency of successful pod binds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms → ~40s
	}, []string{"node"})

	chronosBindRetries = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chronos_scheduler_bind_retries_total",
		Help: "Total number of bind retry attempts",
	})
)

const (
	DefaultBatchIntervalSeconds   = 5
	DefaultBatchSizeLimit         = 100
	DefaultMaxUnscheduledAttempts = 24  // 24 × 5s = 2 minutes before marking as failed
	DefaultCooldownSeconds        = 120 // 2 minutes cooldown before reconsidering failed pods

	// BIND: Enhanced binding defaults
	DefaultMaxConcurrentBinds   = 30 // Worker pool size
	DefaultBindRetryMax         = 3  // Max retry attempts
	DefaultBindTimeoutSeconds   = 30 // Per-bind timeout
	DefaultBindQPS              = 50 // Rate limiting
	DefaultLabelPatchTimeoutSec = 5  // Label patch timeout

	ScheduledByBatchLabelKey          = "scheduled-by-batch"
	ChronosBatchFailedLabelKey        = "chronos-batch-failed"
	UnscheduledAttemptsAnnotationKey  = "chronos.scheduler/unscheduled-attempts"
	BatchFailedReasonAnnotationKey    = "chronos.scheduler/batch-failed-reason"
	BatchFailedTimestampAnnotationKey = "chronos.scheduler/failed-at" // NEW: Cooldown timestamp
)

// OPTIMIZATION: Structs for the new resource request cache
type ResourceRequest struct {
	CPUMillis int64
	MemoryMiB int64
	CachedAt  time.Time
}

type PodResourceCache struct {
	cache       map[types.UID]ResourceRequest
	mutex       sync.RWMutex
	ttl         time.Duration
	maxSize     int
	lastCleanup time.Time
}

type PodSchedulingRequest struct {
	Pod                 *v1.Pod
	ExpectedDuration    int64
	EquivalenceClassKey string // PERFORMANCE: Key to identify the pod's "shape"
}

type NodeAssignment struct {
	NodeName string
	Pods     []*v1.Pod
}

type NodeState struct {
	NodeName            string
	Node                *v1.Node
	CurrentMaxRemaining int64
	AvailableSlots      int
	AvailableCPU        int64
	AvailableMemory     int64
}

type BatchSchedulerConfig struct {
	BatchIntervalSeconds   int
	BatchSizeLimit         int
	MaxUnscheduledAttempts int
	CooldownSeconds        int // NEW: Cooldown period for failed pods

	// BIND: Enhanced binding configuration
	MaxConcurrentBinds   int // Worker pool size
	BindRetryMax         int // Max retry attempts (0 = no retries)
	BindTimeoutSeconds   int // Per-bind timeout
	BindQPS              int // Rate limiting (0 = no limit)
	LabelPatchTimeoutSec int // Timeout for label patching
}

type BatchScheduler struct {
	config        BatchSchedulerConfig
	clientset     kubernetes.Interface
	handle        framework.Handle
	chronos       *Chronos
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	running       bool
	resourceCache *PodResourceCache // OPTIMIZATION: Add cache to the scheduler struct
}

func NewBatchScheduler(handle framework.Handle, chronos *Chronos) *BatchScheduler {
	config := loadBatchSchedulerConfig()
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BatchScheduler{
		config:    config,
		clientset: handle.ClientSet(),
		handle:    handle,
		chronos:   chronos,
		ctx:       ctx,
		cancel:    cancel,
		// OPTIMIZATION: Initialize the cache with cleanup and size limits
		resourceCache: &PodResourceCache{
			cache:       make(map[types.UID]ResourceRequest),
			ttl:         5 * time.Minute,
			maxSize:     40000,
			lastCleanup: time.Now(),
		},
	}
	klog.Infof("🚀 BatchScheduler initialized with config: %+v", config)
	return bs
}

// OPTIMIZATION: Enhanced cached resource calculation method with cleanup and size limits
func (prc *PodResourceCache) GetPodResourceRequest(pod *v1.Pod) (int64, int64) {
	uid := pod.UID

	// Fast path: read lock to check cache
	prc.mutex.RLock()
	if cached, exists := prc.cache[uid]; exists {
		if time.Since(cached.CachedAt) < prc.ttl {
			prc.mutex.RUnlock()
			resourceCacheHits.Inc()
			return cached.CPUMillis, cached.MemoryMiB
		}
	}
	prc.mutex.RUnlock()

	// Periodic cleanup check (every 2 minutes) - must be synchronized
	prc.mutex.RLock()
	shouldCleanup := time.Since(prc.lastCleanup) > 2*time.Minute
	prc.mutex.RUnlock()

	if shouldCleanup {
		go prc.cleanup()
	}

	// Slow path: cache miss, calculate and store
	resourceCacheMisses.Inc()
	var cpuMillis, memMiB int64
	for _, container := range pod.Spec.Containers {
		cpuMillis += container.Resources.Requests.Cpu().MilliValue()
		memMiB += container.Resources.Requests.Memory().Value() / (1024 * 1024)
	}

	prc.mutex.Lock()
	defer prc.mutex.Unlock()

	// Check size limit and evict if necessary - make room for new entry
	if len(prc.cache) >= prc.maxSize {
		prc.evictOldestEntries(prc.maxSize/4 + 1) // Evict 25% + 1 to make room
	}

	prc.cache[uid] = ResourceRequest{
		CPUMillis: cpuMillis,
		MemoryMiB: memMiB,
		CachedAt:  time.Now(),
	}

	resourceCacheSize.Set(float64(len(prc.cache)))
	return cpuMillis, memMiB
}

// OPTIMIZATION: Background cleanup of expired cache entries
func (prc *PodResourceCache) cleanup() {
	prc.mutex.Lock()
	defer prc.mutex.Unlock()

	now := time.Now()
	if time.Since(prc.lastCleanup) < 2*time.Minute {
		return // Another goroutine already did cleanup
	}

	removed := 0
	for uid, entry := range prc.cache {
		if now.Sub(entry.CachedAt) > prc.ttl {
			delete(prc.cache, uid)
			removed++
		}
	}

	prc.lastCleanup = now
	resourceCacheSize.Set(float64(len(prc.cache)))

	if removed > 0 {
		klog.V(4).Infof("🧹 Cache cleanup removed %d expired entries, %d remaining", removed, len(prc.cache))
	}
}

// OPTIMIZATION: Evict oldest entries when cache is full
func (prc *PodResourceCache) evictOldestEntries(count int) {
	if count <= 0 || len(prc.cache) == 0 {
		return
	}

	// Find oldest entries
	type cacheEntry struct {
		uid   types.UID
		entry ResourceRequest
	}

	entries := make([]cacheEntry, 0, len(prc.cache))
	for uid, entry := range prc.cache {
		entries = append(entries, cacheEntry{uid, entry})
	}

	// Sort by age (oldest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].entry.CachedAt.Before(entries[j].entry.CachedAt)
	})

	// Remove oldest entries, but don't exceed available entries
	entriesToRemove := count
	if entriesToRemove > len(entries) {
		entriesToRemove = len(entries)
	}

	removed := 0
	for i := 0; i < entriesToRemove; i++ {
		delete(prc.cache, entries[i].uid)
		resourceCacheEvictions.Inc()
		removed++
	}

	if removed > 0 {
		klog.V(4).Infof("💾 Cache evicted %d oldest entries due to size limit", removed)
	}
}

func loadBatchSchedulerConfig() BatchSchedulerConfig {
	config := BatchSchedulerConfig{
		BatchIntervalSeconds:   DefaultBatchIntervalSeconds,
		BatchSizeLimit:         DefaultBatchSizeLimit,
		MaxUnscheduledAttempts: DefaultMaxUnscheduledAttempts,
		CooldownSeconds:        DefaultCooldownSeconds,

		// BIND: Enhanced binding defaults
		MaxConcurrentBinds:   DefaultMaxConcurrentBinds,
		BindRetryMax:         DefaultBindRetryMax,
		BindTimeoutSeconds:   DefaultBindTimeoutSeconds,
		BindQPS:              DefaultBindQPS,
		LabelPatchTimeoutSec: DefaultLabelPatchTimeoutSec,
	}
	if envVal := os.Getenv("BATCH_INTERVAL_SECONDS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			config.BatchIntervalSeconds = val
		} else {
			klog.Warningf("Invalid value for BATCH_INTERVAL_SECONDS: %q. Using default: %d. Error: %v", envVal, DefaultBatchIntervalSeconds, err)
		}
	}
	if envVal := os.Getenv("BATCH_SIZE_LIMIT"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			config.BatchSizeLimit = val
		} else {
			klog.Warningf("Invalid value for BATCH_SIZE_LIMIT: %q. Using default: %d. Error: %v", envVal, DefaultBatchSizeLimit, err)
		}
	}
	if envVal := os.Getenv("MAX_UNSCHEDULED_ATTEMPTS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			config.MaxUnscheduledAttempts = val
		} else {
			klog.Warningf("Invalid value for MAX_UNSCHEDULED_ATTEMPTS: %q. Using default: %d. Error: %v", envVal, DefaultMaxUnscheduledAttempts, err)
		}
	}
	if envVal := os.Getenv("COOLDOWN_SECONDS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val >= 30 && val <= 900 {
			config.CooldownSeconds = val
		} else {
			klog.Warningf("Invalid value for COOLDOWN_SECONDS: %q (must be 30-900). Using default: %d. Error: %v", envVal, DefaultCooldownSeconds, err)
		}
	}

	// BIND: Enhanced binding configuration from environment
	if envVal := os.Getenv("MAX_CONCURRENT_BINDS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			config.MaxConcurrentBinds = val
		} else {
			klog.Warningf("Invalid value for MAX_CONCURRENT_BINDS: %q. Using default: %d. Error: %v", envVal, DefaultMaxConcurrentBinds, err)
		}
	}
	if envVal := os.Getenv("BIND_RETRY_MAX"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val >= 0 {
			config.BindRetryMax = val
		} else {
			klog.Warningf("Invalid value for BIND_RETRY_MAX: %q. Using default: %d. Error: %v", envVal, DefaultBindRetryMax, err)
		}
	}
	if envVal := os.Getenv("BIND_TIMEOUT_SECONDS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			config.BindTimeoutSeconds = val
		} else {
			klog.Warningf("Invalid value for BIND_TIMEOUT_SECONDS: %q. Using default: %d. Error: %v", envVal, DefaultBindTimeoutSeconds, err)
		}
	}
	if envVal := os.Getenv("BIND_QPS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val >= 0 {
			config.BindQPS = val
		} else {
			klog.Warningf("Invalid value for BIND_QPS: %q. Using default: %d. Error: %v", envVal, DefaultBindQPS, err)
		}
	}
	if envVal := os.Getenv("LABEL_PATCH_TIMEOUT_SEC"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			config.LabelPatchTimeoutSec = val
		} else {
			klog.Warningf("Invalid value for LABEL_PATCH_TIMEOUT_SEC: %q. Using default: %d. Error: %v", envVal, DefaultLabelPatchTimeoutSec, err)
		}
	}

	return config
}

func (bs *BatchScheduler) Start() {
	bs.mu.Lock()
	if bs.running {
		bs.mu.Unlock()
		return
	}
	bs.running = true
	bs.mu.Unlock()
	go bs.cronLoop()
}

// GRACEFUL SHUTDOWN: Stop method to cleanly terminate the batch scheduler
func (bs *BatchScheduler) Stop() {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if !bs.running {
		return
	}
	bs.running = false
	bs.cancel() // Signal the cronLoop to exit
	klog.Infof("🛑 BatchScheduler stopped")
}

// GRACEFUL SHUTDOWN: Updated cronLoop to handle context cancellation
func (bs *BatchScheduler) cronLoop() {
	ticker := time.NewTicker(time.Duration(bs.config.BatchIntervalSeconds) * time.Second)
	defer ticker.Stop()

	klog.Infof("⏰ CRON loop started")

	for {
		select {
		case <-bs.ctx.Done():
			klog.Infof("📴 CRON loop terminated gracefully")
			return
		case <-ticker.C:
			batchRunCounter.Inc()
			timer := prometheus.NewTimer(batchRunDuration)
			bs.processBatch()
			timer.ObserveDuration()
		}
	}
}

func (bs *BatchScheduler) processBatch() {
	pendingPods, err := bs.fetchPendingPods()
	if err != nil || len(pendingPods) == 0 {
		if err != nil {
			klog.Errorf("❌ Error fetching pending pods: %v", err)
		}
		return
	}

	// SAFETY: Store pods in inFlightPods with guaranteed cleanup
	for _, pod := range pendingPods {
		bs.chronos.inFlightPods.Store(pod.UID, true)
	}

	// CRITICAL: Use defer to ensure cleanup happens on ALL exit paths
	defer func() {
		for _, pod := range pendingPods {
			bs.chronos.inFlightPods.Delete(pod.UID)
		}
	}()

	klog.Infof("📦 Starting batch of %d pending pods", len(pendingPods))

	requests := bs.analyzePods(pendingPods)
	if len(requests) == 0 {
		klog.Infof("⚠️ No valid pods found after analysis (check for invalid annotations or nodeSelectors)")
		return
	}

	nodes, err := bs.getAvailableNodes()
	if err != nil || len(nodes) == 0 {
		if err != nil {
			klog.Errorf("❌ Error getting available nodes: %v", err)
		} else {
			klog.Warningf("⚠️ No available nodes to schedule on.")
		}
		return
	}
	nodeStates := bs.getNodeStates(nodes)

	// PERFORMANCE: Use the new high-performance assignment function with pre-filtering
	assignments, unassignedPods := bs.optimizeBatchAssignmentWithPrefiltering(requests, nodeStates)
	scheduledCount, bindFailedPods := bs.executeAssignments(assignments)

	allUnscheduledPods := append(unassignedPods, bindFailedPods...)
	bs.handleUnscheduledPods(allUnscheduledPods)

	podsScheduledCounter.WithLabelValues("success").Add(float64(scheduledCount))
	if len(bindFailedPods) > 0 {
		podsScheduledCounter.WithLabelValues("failure").Add(float64(len(bindFailedPods)))
	}

	klog.V(2).Infof("✅ Batch completed. Scheduled: %d, Bind-Failed: %d, Unassigned: %d.", scheduledCount, len(bindFailedPods), len(unassignedPods))
}

// COOLDOWN: Enhanced fetchPendingPods with cooldown support for failed pods
func (bs *BatchScheduler) fetchPendingPods() ([]*v1.Pod, error) {
	// Fetch all pending pods that haven't been successfully scheduled by us yet.
	// NOTE: We no longer filter out failed pods here - we'll handle cooldown logic manually
	listOptions := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("status.phase", string(v1.PodPending)).String(),
		LabelSelector: "!" + ScheduledByBatchLabelKey,
	}
	podList, err := bs.clientset.CoreV1().Pods("").List(bs.ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending pods: %v", err)
	}

	var eligiblePods []*v1.Pod
	var podsToReset []*v1.Pod
	cooldownCount := 0

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != "" {
			continue // Already has a node, ignore.
		}

		// Only consider pods with duration annotation
		if _, ok := pod.Annotations[JobDurationAnnotation]; !ok {
			continue
		}

		// Check if this pod has previously failed
		if _, isFailed := pod.Labels[ChronosBatchFailedLabelKey]; isFailed {
			// This pod has previously failed. Check if its cooldown has expired.
			failedAtStr, hasTimestamp := pod.Annotations[BatchFailedTimestampAnnotationKey]
			if !hasTimestamp {
				cooldownCount++
				continue // Should not happen, but ignore if it does.
			}
			failedAt, err := time.Parse(time.RFC3339, failedAtStr)
			if err != nil {
				cooldownCount++
				continue // Ignore pods with malformed timestamps.
			}

			if time.Since(failedAt) > time.Duration(bs.config.CooldownSeconds)*time.Second {
				// Cooldown has expired! This pod is now eligible for reconsideration.
				klog.V(3).Infof("🔄 Pod %s/%s cooldown expired (%.1fs). Reconsidering for scheduling.",
					pod.Namespace, pod.Name, time.Since(failedAt).Seconds())
				podsToReset = append(podsToReset, pod)
				eligiblePods = append(eligiblePods, pod)
				cooldownResetCounter.Inc()
			} else {
				cooldownCount++
			}
		} else {
			// This is a standard pending pod that has not failed before.
			eligiblePods = append(eligiblePods, pod)
		}
	}

	// Update cooldown metrics
	cooldownActiveGauge.Set(float64(cooldownCount))

	// Asynchronously reset the state of pods whose cooldown has expired.
	if len(podsToReset) > 0 {
		go bs.resetFailedPods(podsToReset)
	}

	// Apply batch size limit to the final list of eligible pods.
	if len(eligiblePods) > bs.config.BatchSizeLimit {
		sort.Slice(eligiblePods, func(i, j int) bool {
			return eligiblePods[i].CreationTimestamp.Time.Before(eligiblePods[j].CreationTimestamp.Time)
		})
		eligiblePods = eligiblePods[:bs.config.BatchSizeLimit]
	}
	return eligiblePods, nil
}

// COOLDOWN: Asynchronously resets the labels and annotations of pods whose cooldown has expired
// Uses strategic merge patch to avoid update conflicts
func (bs *BatchScheduler) resetFailedPods(pods []*v1.Pod) {
	var wg sync.WaitGroup
	for _, pod := range pods {
		wg.Add(1)
		go func(p *v1.Pod) {
			defer wg.Done()

			// Build strategic merge patch payload to remove failure markers
			patchPayload := map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						ChronosBatchFailedLabelKey: nil, // null removes the label
					},
					"annotations": map[string]any{
						UnscheduledAttemptsAnnotationKey:  nil, // null removes the annotation
						BatchFailedReasonAnnotationKey:    nil,
						BatchFailedTimestampAnnotationKey: nil,
					},
				},
			}

			patchBytes, err := json.Marshal(patchPayload)
			if err != nil {
				klog.Errorf("⚠️ Failed to marshal patch for pod %s/%s cooldown reset: %v", p.Namespace, p.Name, err)
				return
			}

			_, patchErr := bs.clientset.CoreV1().Pods(p.Namespace).Patch(
				bs.ctx, p.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
			if patchErr != nil {
				klog.Errorf("⚠️ Failed to reset pod %s/%s after cooldown: %v", p.Namespace, p.Name, patchErr)
			} else {
				klog.V(3).Infof("✅ Successfully reset failed pod %s/%s for retry", p.Namespace, p.Name)
			}
		}(pod)
	}
	wg.Wait()
}

func (bs *BatchScheduler) analyzePods(pods []*v1.Pod) []PodSchedulingRequest {
	requests := make([]PodSchedulingRequest, 0, len(pods))
	for _, pod := range pods {
		if annotation, exists := pod.Annotations[JobDurationAnnotation]; exists {
			if duration, err := strconv.ParseFloat(annotation, 64); err == nil && duration > 0 {
				if bs.validateNodeSelector(pod) {
					nodeSelectorsValidated.WithLabelValues("valid").Inc()
					requests = append(requests, PodSchedulingRequest{
						Pod:                 pod,
						ExpectedDuration:    int64(math.Round(duration)),
						EquivalenceClassKey: bs.getEquivalenceClassKey(pod),
					})
				} else {
					nodeSelectorsValidated.WithLabelValues("invalid").Inc()
					klog.Warningf("⚠️ Pod %s/%s has an invalid nodeSelector and will be ignored by the batch scheduler. Selector: %v",
						pod.Namespace, pod.Name, pod.Spec.NodeSelector)
				}
			}
		}
	}
	return requests
}

func (bs *BatchScheduler) validateNodeSelector(pod *v1.Pod) bool {
	if len(pod.Spec.NodeSelector) == 0 {
		return true
	}
	for key, value := range pod.Spec.NodeSelector {
		if key == "" || value == "" {
			return false
		}
	}
	return true
}

// CACHE-OPTIMIZED with reliability fallback - user's simpler approach
func (bs *BatchScheduler) getAvailableNodes() ([]*v1.Node, error) {
	// PRIMARY: Try cache-optimized approach first
	nodeInfoList, err := bs.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		// FALLBACK: Cache failure - use direct API with backoff as safety net
		cacheReliabilityCounter.WithLabelValues("cache_miss").Inc()
		klog.Warningf("⚠️ Cache read failed (%v), falling back to direct API call with exponential backoff", err)
		return bs.getAvailableNodesWithBackoff()
	}

	var availableNodes []*v1.Node
	for _, nodeInfo := range nodeInfoList {
		node := nodeInfo.Node()
		if node.Spec.Unschedulable {
			continue
		}
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
				availableNodes = append(availableNodes, node)
				break
			}
		}
	}

	cacheReliabilityCounter.WithLabelValues("cache_hit").Inc()
	return availableNodes, nil
}

// FALLBACK with exponential backoff for reliability - user's clean approach
func (bs *BatchScheduler) getAvailableNodesWithBackoff() ([]*v1.Node, error) {
	const maxRetries = 3
	baseBackoff := 100 * time.Millisecond
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		nodeList, err := bs.clientset.CoreV1().Nodes().List(bs.ctx, metav1.ListOptions{})
		if err == nil {
			var availableNodes []*v1.Node
			for i := range nodeList.Items {
				node := &nodeList.Items[i]
				if !node.Spec.Unschedulable {
					for _, cond := range node.Status.Conditions {
						if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
							availableNodes = append(availableNodes, node)
							break
						}
					}
				}
			}
			cacheReliabilityCounter.WithLabelValues("api_fallback_success").Inc()
			return availableNodes, nil
		}
		lastErr = err
		time.Sleep(baseBackoff)
		baseBackoff *= 2 // Exponential increase
	}

	cacheReliabilityCounter.WithLabelValues("api_fallback_failed").Inc()
	return nil, fmt.Errorf("failed to get nodes from API after %d retries: %w", maxRetries, lastErr)
}

// PERFORMANCE: Enhanced NodeState with cached NodeInfo to eliminate redundant API calls
type EnhancedNodeState struct {
	*NodeState
	NodeInfo *framework.NodeInfo // Cache the NodeInfo to avoid repeated API calls
}

func (bs *BatchScheduler) getNodeStates(nodes []*v1.Node) map[string]*EnhancedNodeState {
	nodeStates := make(map[string]*EnhancedNodeState)
	for _, node := range nodes {
		nodeInfo, err := bs.handle.SnapshotSharedLister().NodeInfos().Get(node.Name)
		if err != nil {
			klog.Warningf("⚠️ Could not get node info from cache for %s: %v. Skipping node.", node.Name, err)
			continue
		}

		capacity := int(node.Status.Allocatable.Pods().Value())
		availableSlots := capacity - len(nodeInfo.Pods)
		if availableSlots < 0 {
			availableSlots = 0
		}

		allocatableCPU := node.Status.Allocatable.Cpu().MilliValue()
		allocatableMemory := node.Status.Allocatable.Memory().Value() / (1024 * 1024)

		requestedCPU := int64(0)
		requestedMemory := int64(0)
		for _, podInfo := range nodeInfo.Pods {
			// OPTIMIZATION: Use the cache for already scheduled pods
			podCPU, podMem := bs.resourceCache.GetPodResourceRequest(podInfo.Pod)
			requestedCPU += podCPU
			requestedMemory += podMem
		}

		availableCPU := allocatableCPU - requestedCPU
		availableMemory := allocatableMemory - requestedMemory
		if availableCPU < 0 {
			availableCPU = 0
		}
		if availableMemory < 0 {
			availableMemory = 0
		}

		nodeStates[node.Name] = &EnhancedNodeState{
			NodeState: &NodeState{
				NodeName:            node.Name,
				Node:                node,
				CurrentMaxRemaining: bs.chronos.calculateMaxRemainingTimeOptimized(nodeInfo.Pods),
				AvailableSlots:      availableSlots,
				AvailableCPU:        availableCPU,
				AvailableMemory:     availableMemory,
			},
			NodeInfo: nodeInfo, // PERFORMANCE: Cache NodeInfo to eliminate redundant API calls
		}
	}
	return nodeStates
}

func (bs *BatchScheduler) optimizeBatchAssignment(requests []PodSchedulingRequest, nodeStates map[string]*EnhancedNodeState) (map[string]*NodeAssignment, []*v1.Pod) {
	sort.Slice(requests, func(i, j int) bool {
		prioI := 0
		if requests[i].Pod.Spec.Priority != nil {
			prioI = int(*requests[i].Pod.Spec.Priority)
		}
		prioJ := 0
		if requests[j].Pod.Spec.Priority != nil {
			prioJ = int(*requests[j].Pod.Spec.Priority)
		}

		if prioI != prioJ {
			return prioI > prioJ
		}
		return requests[i].ExpectedDuration > requests[j].ExpectedDuration
	})

	assignments := make(map[string]*NodeAssignment)
	assignedPods := make(map[string]bool)

	for _, req := range requests {
		// OPTIMIZATION: Use the cache for unscheduled pods
		podRequestCPU, podRequestMemory := bs.resourceCache.GetPodResourceRequest(req.Pod)

		bestNodeName := ""
		bestScore := int64(math.MinInt64)

		for name, enhancedState := range nodeStates {
			state := enhancedState.NodeState
			if state.AvailableSlots <= 0 || state.AvailableCPU < podRequestCPU || state.AvailableMemory < podRequestMemory {
				continue
			}
			if !bs.podFitsNodeSelector(req.Pod, state.Node) {
				continue
			}
			if !bs.preFlightCheck(req.Pod, name) {
				continue
			}

			// PERFORMANCE: Use cached NodeInfo instead of redundant API call
			score := bs.chronos.CalculateOptimizedScore(req.Pod, enhancedState.NodeInfo, state.CurrentMaxRemaining, req.ExpectedDuration)
			if score > bestScore {
				bestScore = score
				bestNodeName = name
			}
		}

		if bestNodeName != "" {
			if assignments[bestNodeName] == nil {
				assignments[bestNodeName] = &NodeAssignment{NodeName: bestNodeName}
			}
			assignments[bestNodeName].Pods = append(assignments[bestNodeName].Pods, req.Pod)
			assignedPods[req.Pod.Name] = true

			enhancedState := nodeStates[bestNodeName]
			state := enhancedState.NodeState
			state.AvailableSlots--
			state.AvailableCPU -= podRequestCPU
			state.AvailableMemory -= podRequestMemory
			if req.ExpectedDuration > state.CurrentMaxRemaining {
				state.CurrentMaxRemaining = req.ExpectedDuration
			}
		}
	}

	var unassignedPods []*v1.Pod
	for _, req := range requests {
		if !assignedPods[req.Pod.Name] {
			unassignedPods = append(unassignedPods, req.Pod)
		}
	}
	return assignments, unassignedPods
}

// PERFORMANCE: New high-performance assignment function with node pre-filtering
func (bs *BatchScheduler) optimizeBatchAssignmentWithPrefiltering(requests []PodSchedulingRequest, nodeStates map[string]*EnhancedNodeState) (map[string]*NodeAssignment, []*v1.Pod) {
	// STEP 1: Pre-filter nodes by unique equivalence classes (one-time cost)
	klog.V(4).Infof("Starting pre-filtering for %d unique pod classes...", len(bs.getUniqueEquivalenceClasses(requests)))
	classToEligibleNodes := bs.preFilterNodesByClass(requests, nodeStates)

	// STEP 2: Sort all pods by priority and duration
	sort.Slice(requests, func(i, j int) bool {
		prioI := 0
		if requests[i].Pod.Spec.Priority != nil {
			prioI = int(*requests[i].Pod.Spec.Priority)
		}
		prioJ := 0
		if requests[j].Pod.Spec.Priority != nil {
			prioJ = int(*requests[j].Pod.Spec.Priority)
		}
		if prioI != prioJ {
			return prioI > prioJ
		}
		return requests[i].ExpectedDuration > requests[j].ExpectedDuration
	})

	// STEP 3: Use the pre-filtered node lists for each pod
	assignments := make(map[string]*NodeAssignment)
	assignedPods := make(map[string]bool)

	for _, req := range requests {
		podRequestCPU, podRequestMemory := bs.resourceCache.GetPodResourceRequest(req.Pod)
		bestNodeName := ""
		bestScore := int64(math.MinInt64)

		// CRITICAL OPTIMIZATION: Loop only over the small list of pre-filtered, compatible nodes!
		eligibleNodeNames := classToEligibleNodes[req.EquivalenceClassKey]
		for _, nodeName := range eligibleNodeNames {
			state := nodeStates[nodeName]

			if state.AvailableSlots <= 0 || state.AvailableCPU < podRequestCPU || state.AvailableMemory < podRequestMemory {
				continue
			}

			score := bs.chronos.CalculateOptimizedScore(req.Pod, state.NodeInfo, state.CurrentMaxRemaining, req.ExpectedDuration)
			if score > bestScore {
				bestScore = score
				bestNodeName = nodeName
			}
		}

		if bestNodeName != "" {
			if assignments[bestNodeName] == nil {
				assignments[bestNodeName] = &NodeAssignment{NodeName: bestNodeName}
			}
			assignments[bestNodeName].Pods = append(assignments[bestNodeName].Pods, req.Pod)
			assignedPods[req.Pod.Name] = true

			state := nodeStates[bestNodeName]
			state.AvailableSlots--
			state.AvailableCPU -= podRequestCPU
			state.AvailableMemory -= podRequestMemory
			if req.ExpectedDuration > state.CurrentMaxRemaining {
				state.CurrentMaxRemaining = req.ExpectedDuration
			}
		}
	}

	var unassignedPods []*v1.Pod
	for _, req := range requests {
		if !assignedPods[req.Pod.Name] {
			unassignedPods = append(unassignedPods, req.Pod)
		}
	}
	return assignments, unassignedPods
}

// BIND: Error classification for better observability
func classifyError(err error) string {
	if err == nil {
		return "none"
	}
	errStr := err.Error()
	if strings.Contains(errStr, "timeout") {
		return "timeout"
	}
	if strings.Contains(errStr, "conflict") {
		return "conflict"
	}
	if strings.Contains(errStr, "not found") {
		return "not_found"
	}
	if strings.Contains(errStr, "forbidden") {
		return "forbidden"
	}
	if strings.Contains(errStr, "unauthorized") {
		return "unauthorized"
	}
	return "unknown"
}

// BIND: Retry pod binding with exponential backoff
func (bs *BatchScheduler) bindPodWithRetry(p *v1.Pod, nodeName string) error {
	attempts := bs.config.BindRetryMax + 1 // Fix: +1 for total attempts vs retries
	if attempts <= 0 {
		attempts = 1
	}
	backoff := 200 * time.Millisecond
	var lastErr error

	for i := 0; i < attempts; i++ {
		if i > 0 {
			chronosBindRetries.Inc()
		}

		ctx, cancel := context.WithTimeout(bs.ctx, time.Duration(bs.config.BindTimeoutSeconds)*time.Second)
		err := bs.clientset.CoreV1().Pods(p.Namespace).Bind(ctx, &v1.Binding{
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
			Target:     v1.ObjectReference{Kind: "Node", Name: nodeName},
		}, metav1.CreateOptions{})
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		if i < attempts-1 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return lastErr
}

// BIND: Efficient label patching using MergePatch
func (bs *BatchScheduler) patchPodLabels(ctx context.Context, ns, name string, labels map[string]string) error {
	payload := map[string]any{
		"metadata": map[string]any{
			"labels": labels,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal patch for pod %s/%s: %w", ns, name, err)
	}
	_, patchErr := bs.clientset.CoreV1().Pods(ns).Patch(ctx, name, types.MergePatchType, b, metav1.PatchOptions{})
	return patchErr
}

// PERFORMANCE: New helper to pre-compute eligible nodes for each class
func (bs *BatchScheduler) preFilterNodesByClass(requests []PodSchedulingRequest, nodeStates map[string]*EnhancedNodeState) map[string][]string {
	classToEligibleNodes := make(map[string][]string)
	uniqueClasses := bs.getUniqueEquivalenceClasses(requests)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for classKey, representativePod := range uniqueClasses {
		wg.Add(1)
		go func(key string, pod *v1.Pod) {
			defer wg.Done()
			var eligibleNodes []string
			for nodeName := range nodeStates {
				if bs.preFlightCheck(pod, nodeName) {
					eligibleNodes = append(eligibleNodes, nodeName)
				}
			}
			mu.Lock()
			classToEligibleNodes[key] = eligibleNodes
			mu.Unlock()
		}(classKey, representativePod)
	}

	wg.Wait()
	return classToEligibleNodes
}

// PERFORMANCE: New helper to group pods by equivalence class
func (bs *BatchScheduler) getUniqueEquivalenceClasses(requests []PodSchedulingRequest) map[string]*v1.Pod {
	uniqueClasses := make(map[string]*v1.Pod)
	for _, req := range requests {
		if _, exists := uniqueClasses[req.EquivalenceClassKey]; !exists {
			uniqueClasses[req.EquivalenceClassKey] = req.Pod
		}
	}
	return uniqueClasses
}

// PERFORMANCE: New helper to generate a key for a pod's scheduling requirements
func (bs *BatchScheduler) getEquivalenceClassKey(pod *v1.Pod) string {
	var b strings.Builder
	// A simple but effective way to create a unique key for a pod's "shape"
	b.WriteString(fmt.Sprintf("sel:%v;", pod.Spec.NodeSelector))
	b.WriteString(fmt.Sprintf("aff:%v;", pod.Spec.Affinity))
	b.WriteString(fmt.Sprintf("tol:%v;", pod.Spec.Tolerations))

	hash := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(hash[:])
}

func (bs *BatchScheduler) preFlightCheck(pod *v1.Pod, nodeName string) bool {
	nodeInfo, err := bs.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		klog.V(4).Infof("Could not get node info for pre-flight check on node %s: %v", nodeName, err)
		return false
	}
	status := bs.handle.RunFilterPlugins(bs.ctx, framework.NewCycleState(), pod, nodeInfo)
	return status.IsSuccess()
}

func (bs *BatchScheduler) podFitsNodeSelector(pod *v1.Pod, node *v1.Node) bool {
	if pod.Spec.NodeSelector == nil {
		return true
	}
	for key, value := range pod.Spec.NodeSelector {
		nodeLabelValue, ok := node.Labels[key]
		if !ok || nodeLabelValue != value {
			return false
		}
	}
	return true
}

func (bs *BatchScheduler) checkForUnsatisfiablePods(unassignedPods []*v1.Pod, nodeStates map[string]*NodeState) {
	for _, pod := range unassignedPods {
		canBeScheduledOnAnyNode := false
		for _, state := range nodeStates {
			podCPU, podMem := bs.resourceCache.GetPodResourceRequest(pod) // Use cache
			if bs.podFitsNodeSelector(pod, state.Node) &&
				bs.preFlightCheck(pod, state.NodeName) &&
				state.AvailableSlots > 0 &&
				state.AvailableCPU >= podCPU &&
				state.AvailableMemory >= podMem {
				canBeScheduledOnAnyNode = true
				break
			}
		}

		if !canBeScheduledOnAnyNode {
			klog.Warningf("⚠️ Pod %s/%s could not be scheduled on any available node due to placement constraints (check affinity, tolerations, or resource requests).",
				pod.Namespace, pod.Name)
		}
	}
}

// CONCURRENCY: Rate-limited pod binding to prevent API server overload
// BIND: Enhanced executeAssignments with worker pool pattern and proper error handling
func (bs *BatchScheduler) executeAssignments(assignments map[string]*NodeAssignment) (uint64, []*v1.Pod) {
	type task struct {
		pod      *v1.Pod
		nodeName string
	}

	// Flatten all assignments into a task list
	tasks := make([]task, 0, 1024)
	for _, a := range assignments {
		for _, p := range a.Pods {
			tasks = append(tasks, task{pod: p, nodeName: a.NodeName})
		}
	}
	if len(tasks) == 0 {
		return 0, nil
	}

	maxWorkers := bs.config.MaxConcurrentBinds
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

	taskCh := make(chan task, len(tasks))
	var (
		scheduledCount atomic.Uint64
		failedMu       sync.Mutex
		failedPods     []*v1.Pod
		wg             sync.WaitGroup
	)

	// Optional QPS limiter (simple rate limiting with time.Ticker)
	var ticker *time.Ticker
	if bs.config.BindQPS > 0 {
		ticker = time.NewTicker(time.Second / time.Duration(bs.config.BindQPS))
		defer ticker.Stop()
	}

	worker := func() {
		defer wg.Done()
		for tk := range taskCh {
			if ticker != nil {
				// Rate limiting with ticker
				select {
				case <-ticker.C:
					// Rate limit token acquired
				case <-bs.ctx.Done():
					return
				}
			}

			start := time.Now()
			err := bs.bindPodWithRetry(tk.pod, tk.nodeName)
			duration := time.Since(start)

			if err != nil {
				reason := classifyError(err)
				klog.Errorf("❌ Bind failed for pod %s/%s to node %s: %v", tk.pod.Namespace, tk.pod.Name, tk.nodeName, err)

				failedMu.Lock()
				failedPods = append(failedPods, tk.pod)
				failedMu.Unlock()

				chronosBindFailures.WithLabelValues(reason).Inc()
				continue
			}

			// Patch scheduled label (best-effort)
			lctx, cancel := context.WithTimeout(bs.ctx, time.Duration(bs.config.LabelPatchTimeoutSec)*time.Second)
			if patchErr := bs.patchPodLabels(lctx, tk.pod.Namespace, tk.pod.Name, map[string]string{
				ScheduledByBatchLabelKey: "true",
			}); patchErr != nil {
				klog.Warningf("⚠️ Failed to patch label for pod %s/%s: %v", tk.pod.Namespace, tk.pod.Name, patchErr)
			}
			cancel()

			klog.V(2).Infof("✅ Bound pod %s/%s to node %s in %v", tk.pod.Namespace, tk.pod.Name, tk.nodeName, duration)
			scheduledCount.Add(1)
			chronosBindSuccess.WithLabelValues(tk.nodeName).Inc()
			chronosBindDuration.WithLabelValues(tk.nodeName).Observe(duration.Seconds())
		}
	}

	// Start workers
	wg.Add(maxWorkers)
	for i := 0; i < maxWorkers; i++ {
		go worker()
	}

	// Enqueue tasks with graceful shutdown handling
enqueueLoop:
	for _, t := range tasks {
		select {
		case taskCh <- t:
		case <-bs.ctx.Done():
			klog.Warning("⚠️ Scheduler shutting down during executeAssignments")
			break enqueueLoop
		}
	}
	close(taskCh)

	wg.Wait()
	return scheduledCount.Load(), failedPods
}

func (bs *BatchScheduler) labelPodAsScheduled(pod *v1.Pod) error {
	latestPod, err := bs.clientset.CoreV1().Pods(pod.Namespace).Get(bs.ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get latest pod object for labeling: %w", err)
	}

	if latestPod.Labels == nil {
		latestPod.Labels = make(map[string]string)
	}
	latestPod.Labels[ScheduledByBatchLabelKey] = "true"

	_, updateErr := bs.clientset.CoreV1().Pods(latestPod.Namespace).Update(bs.ctx, latestPod, metav1.UpdateOptions{})
	if updateErr != nil {
		return fmt.Errorf("failed to update pod label: %w", updateErr)
	}
	return nil
}

func (bs *BatchScheduler) handleUnscheduledPods(pods []*v1.Pod) {
	var wg sync.WaitGroup
	for _, pod := range pods {
		wg.Add(1)
		go func(p *v1.Pod) {
			defer wg.Done()
			latestPod, err := bs.clientset.CoreV1().Pods(p.Namespace).Get(bs.ctx, p.Name, metav1.GetOptions{})
			if err != nil {
				klog.Errorf("⚠️ Failed to get latest pod object for retry handling: %v", err)
				return
			}

			if latestPod.Annotations == nil {
				latestPod.Annotations = make(map[string]string)
			}

			attemptsStr := latestPod.Annotations[UnscheduledAttemptsAnnotationKey]
			attempts, _ := strconv.Atoi(attemptsStr)
			attempts++

			if attempts >= bs.config.MaxUnscheduledAttempts {
				klog.Warningf("🚫 pod %s/%s has exceeded max retry attempts (%d) and will be marked as failed.",
					latestPod.Namespace, latestPod.Name, bs.config.MaxUnscheduledAttempts)
				if latestPod.Labels == nil {
					latestPod.Labels = make(map[string]string)
				}
				latestPod.Labels[ChronosBatchFailedLabelKey] = "true"
				latestPod.Annotations[BatchFailedReasonAnnotationKey] = fmt.Sprintf("Pod failed to schedule after %d attempts", attempts)
				// COOLDOWN: Add the failure timestamp to start the cooldown period
				latestPod.Annotations[BatchFailedTimestampAnnotationKey] = time.Now().Format(time.RFC3339)
			} else {
				latestPod.Annotations[UnscheduledAttemptsAnnotationKey] = strconv.Itoa(attempts)
				klog.V(3).Infof("🔄 pod %s/%s unscheduled, attempt %d/%d.",
					latestPod.Namespace, latestPod.Name, attempts, bs.config.MaxUnscheduledAttempts)
			}

			_, updateErr := bs.clientset.CoreV1().Pods(latestPod.Namespace).Update(bs.ctx, latestPod, metav1.UpdateOptions{})
			if updateErr != nil {
				klog.Errorf("⚠️ Failed to update pod %s/%s with retry status: %v", latestPod.Namespace, latestPod.Name, updateErr)
			}
		}(pod)
	}
	wg.Wait()
}

// OPTIMIZATION: Helper method for tests - mark pod as batch failed
func (bs *BatchScheduler) markPodAsBatchFailed(pod *v1.Pod) {
	latestPod, err := bs.clientset.CoreV1().Pods(pod.Namespace).Get(bs.ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("⚠️ Failed to get latest pod object for batch failure marking: %v", err)
		return
	}

	if latestPod.Labels == nil {
		latestPod.Labels = make(map[string]string)
	}
	latestPod.Labels[ChronosBatchFailedLabelKey] = "true"

	if latestPod.Annotations == nil {
		latestPod.Annotations = make(map[string]string)
	}
	latestPod.Annotations[BatchFailedReasonAnnotationKey] = "Marked as failed by batch scheduler"

	_, updateErr := bs.clientset.CoreV1().Pods(latestPod.Namespace).Update(bs.ctx, latestPod, metav1.UpdateOptions{})
	if updateErr != nil {
		klog.Errorf("⚠️ Failed to update pod %s/%s with batch failure status: %v", latestPod.Namespace, latestPod.Name, updateErr)
	}
}

// OPTIMIZATION: Helper method for tests - get unique node selectors from requests
func (bs *BatchScheduler) getUniqueNodeSelectors(requests []PodSchedulingRequest) []string {
	selectorSet := make(map[string]bool)

	for _, req := range requests {
		var selectorStr string
		if len(req.Pod.Spec.NodeSelector) > 0 {
			selectorStr = bs.nodeSelectorToString(req.Pod.Spec.NodeSelector)
		} else {
			selectorStr = "" // Empty selector for pods without nodeSelector
		}
		selectorSet[selectorStr] = true
	}

	var uniqueSelectors []string
	for selector := range selectorSet {
		uniqueSelectors = append(uniqueSelectors, selector)
	}

	return uniqueSelectors
}

// OPTIMIZATION: Helper method for tests - convert node selector to string
func (bs *BatchScheduler) nodeSelectorToString(nodeSelector map[string]string) string {
	if len(nodeSelector) == 0 {
		return ""
	}

	var pairs []string
	keys := make([]string, 0, len(nodeSelector))
	for k := range nodeSelector {
		keys = append(keys, k)
	}
	sort.Strings(keys) // Ensure consistent ordering

	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, nodeSelector[k]))
	}

	return strings.Join(pairs, ",")
}

// OPTIMIZATION: Helper method for tests - check if node matches selector string
func (bs *BatchScheduler) nodeMatchesSelector(node *v1.Node, selectorStr string) bool {
	if selectorStr == "" {
		return true
	}

	// Parse the selector string into a map
	selector := make(map[string]string)
	pairs := strings.Split(selectorStr, ",")
	for _, pair := range pairs {
		if strings.Contains(pair, "=") {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				selector[parts[0]] = parts[1]
			} else {
				return false // Invalid format
			}
		} else if pair != "" {
			return false // Invalid format
		}
	}

	return bs.podFitsNodeSelector(&v1.Pod{Spec: v1.PodSpec{NodeSelector: selector}}, node)
}
