#!/bin/bash

echo "🎯 Chronos Scheduler - Complete Analysis Tool"
echo "============================================="

# Configuration
POD_NAMESPACE=${1:-default}
SCHEDULER_NAMESPACE=${2:-kube-system}
SCHEDULER_NAME="chronos-kubernetes-scheduler"
TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')

# Check if namespace provided
if [ "$#" -eq 0 ]; then
    echo "Usage: $0 <pod-namespace> [scheduler-namespace]"
    echo ""
    echo "Examples:"
    echo "  $0 production                    # Pods in 'production', scheduler in 'kube-system'"
    echo "  $0 production scheduler-ns       # Pods in 'production', scheduler in 'scheduler-ns'"
    echo "  $0 default chronos-system        # Pods in 'default', scheduler in 'chronos-system'"
    echo ""
    echo "Parameters:"
    echo "  pod-namespace:       Namespace containing pods to analyze (required)"
    echo "  scheduler-namespace: Namespace containing Chronos scheduler (default: kube-system)"
    exit 1
fi

# Check if namespaces exist
if ! kubectl get namespace "$POD_NAMESPACE" > /dev/null 2>&1; then
    echo "❌ Pod namespace '$POD_NAMESPACE' does not exist!"
    exit 1
fi

if ! kubectl get namespace "$SCHEDULER_NAMESPACE" > /dev/null 2>&1; then
    echo "❌ Scheduler namespace '$SCHEDULER_NAMESPACE' does not exist!"
    exit 1
fi

echo "📍 Analyzing pods in namespace: $POD_NAMESPACE"
echo "📍 Scheduler in namespace: $SCHEDULER_NAMESPACE"
echo "📍 Timestamp: $TIMESTAMP"
echo "📍 Expected scheduler: $SCHEDULER_NAME"
echo ""

# Helper function to calculate percentage
calc_percentage() {
    if [ $2 -eq 0 ]; then
        echo "0"
    else
        echo "scale=1; $1 * 100 / $2" | bc 2>/dev/null || echo "0"
    fi
}

# ================================================================
# 1. SCHEDULER HEALTH CHECK
# ================================================================
echo "🏥 SCHEDULER HEALTH CHECK"
echo "========================="

SCHEDULER_POD=$(kubectl get pods -n $SCHEDULER_NAMESPACE -l app.kubernetes.io/name=chronos-kubernetes-scheduler --no-headers 2>/dev/null | head -1)

if [ -z "$SCHEDULER_POD" ]; then
    echo "❌ CRITICAL: Chronos scheduler pod not found!"
    echo "   Make sure the scheduler is deployed in '$SCHEDULER_NAMESPACE' namespace"
    echo "   with label app.kubernetes.io/name=chronos-kubernetes-scheduler"
    exit 1
fi

SCHEDULER_STATUS=$(echo $SCHEDULER_POD | awk '{print $3}')
SCHEDULER_NAME_POD=$(echo $SCHEDULER_POD | awk '{print $1}')

echo "📍 Scheduler pod: $SCHEDULER_NAME_POD"
echo "📊 Status: $SCHEDULER_STATUS"

if [ "$SCHEDULER_STATUS" = "Running" ]; then
    echo "✅ Scheduler is healthy and running"
    
    # Check resource usage
    RESOURCE_INFO=$(kubectl top pod -n $SCHEDULER_NAMESPACE $SCHEDULER_NAME_POD --no-headers 2>/dev/null)
    if [ ! -z "$RESOURCE_INFO" ]; then
        CPU_USAGE=$(echo $RESOURCE_INFO | awk '{print $2}')
        MEM_USAGE=$(echo $RESOURCE_INFO | awk '{print $3}')
        echo "📊 Resource usage: CPU=$CPU_USAGE, Memory=$MEM_USAGE"
    fi
    
    # Check recent activity
    RECENT_LOGS=$(kubectl logs -n $SCHEDULER_NAMESPACE $SCHEDULER_NAME_POD --tail=50 --since=10m 2>/dev/null | wc -l)
    if [ $RECENT_LOGS -gt 0 ]; then
        echo "✅ Recent scheduler activity detected ($RECENT_LOGS log entries)"
    else
        echo "⚠️  No recent scheduler activity in last 10 minutes"
    fi
else
    echo "❌ CRITICAL: Scheduler is not running properly"
    kubectl describe pod -n $SCHEDULER_NAMESPACE $SCHEDULER_NAME_POD | tail -10
    exit 1
fi

echo ""

# ================================================================
# 2. POD ANALYSIS
# ================================================================
echo "📦 POD ANALYSIS"
echo "==============="

# Get all pods in namespace
ALL_PODS=$(kubectl get pods -n $POD_NAMESPACE --no-headers 2>/dev/null)
TOTAL_PODS=$(echo "$ALL_PODS" | wc -l)

if [ $TOTAL_PODS -eq 0 ] || [ "$ALL_PODS" = "" ]; then
    echo "❌ No pods found in namespace '$POD_NAMESPACE'"
    exit 0
fi

# Pod status counts
RUNNING=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l)
PENDING=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Pending --no-headers 2>/dev/null | wc -l)
FAILED=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Failed --no-headers 2>/dev/null | wc -l)
SUCCEEDED=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Succeeded --no-headers 2>/dev/null | wc -l)

# Calculate percentages
RUNNING_PCT=$(calc_percentage $RUNNING $TOTAL_PODS)
PENDING_PCT=$(calc_percentage $PENDING $TOTAL_PODS)
SUCCESS_RATE=$(calc_percentage $RUNNING $TOTAL_PODS)

echo "📊 Pod Status Overview:"
echo "   Total pods: $TOTAL_PODS"
echo "   🟢 Running: $RUNNING ($RUNNING_PCT%)"
echo "   🟡 Pending: $PENDING ($PENDING_PCT%)"
echo "   ✅ Succeeded: $SUCCEEDED"
echo "   ❌ Failed: $FAILED"
echo "   📈 Success Rate: $SUCCESS_RATE%"

echo ""

# ================================================================
# 3. CHRONOS SCHEDULER USAGE
# ================================================================
echo "🎯 CHRONOS SCHEDULER USAGE"
echo "=========================="

# Count pods using Chronos
CHRONOS_PODS=$(kubectl get pods -n $POD_NAMESPACE -o jsonpath='{.items[?(@.spec.schedulerName=="'$SCHEDULER_NAME'")].metadata.name}' 2>/dev/null | wc -w)
DEFAULT_PODS=$(kubectl get pods -n $POD_NAMESPACE -o jsonpath='{.items[?(@.spec.schedulerName=="default-scheduler")].metadata.name}' 2>/dev/null | wc -w)
NO_SCHEDULER=$(kubectl get pods -n $POD_NAMESPACE -o jsonpath='{.items[?(@.spec.schedulerName=="")].metadata.name}' 2>/dev/null | wc -w)

CHRONOS_PCT=$(calc_percentage $CHRONOS_PODS $TOTAL_PODS)
DEFAULT_PCT=$(calc_percentage $DEFAULT_PODS $TOTAL_PODS)

echo "📊 Scheduler Distribution:"
echo "   🎯 Chronos scheduler: $CHRONOS_PODS ($CHRONOS_PCT%)"
echo "   🔷 Default scheduler: $DEFAULT_PODS ($DEFAULT_PCT%)"
echo "   ❓ No scheduler specified: $NO_SCHEDULER"

if [ $CHRONOS_PODS -eq $TOTAL_PODS ]; then
    echo "✅ EXCELLENT: All pods using Chronos scheduler"
elif [ $CHRONOS_PODS -gt 0 ]; then
    echo "⚠️  WARNING: Only $CHRONOS_PODS/$TOTAL_PODS pods using Chronos"
else
    echo "❌ CRITICAL: No pods using Chronos scheduler!"
fi

echo ""

# ================================================================
# 4. ANNOTATION ANALYSIS
# ================================================================
echo "📝 ANNOTATION USAGE ANALYSIS"
echo "============================"

WITH_ANNOTATION=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) | .metadata.name' | wc -l)
WITHOUT_ANNOTATION=$((TOTAL_PODS - WITH_ANNOTATION))

ANNOTATION_PCT=$(calc_percentage $WITH_ANNOTATION $TOTAL_PODS)

echo "📊 Duration Annotation Usage:"
echo "   ✅ With annotation: $WITH_ANNOTATION ($ANNOTATION_PCT%)"
echo "   ❌ Without annotation: $WITHOUT_ANNOTATION"

if [ $WITH_ANNOTATION -eq $TOTAL_PODS ]; then
    echo "✅ EXCELLENT: All pods have duration annotations"
elif [ $WITH_ANNOTATION -gt 0 ]; then
    echo "⚠️  WARNING: $WITHOUT_ANNOTATION pods lack duration annotations"
    echo "   → These pods fall back to NodeResourcesFit scoring only"
else
    echo "❌ CRITICAL: No pods have duration annotations!"
    echo "   → Chronos time-based optimization not working!"
fi

# Show annotation values distribution if any exist
if [ $WITH_ANNOTATION -gt 0 ]; then
    echo ""
    echo "📊 Duration Distribution:"
    kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '
    .items[] | 
    select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) | 
    .metadata.annotations["scheduling.workload.io/expected-duration-seconds"]' | sort -n | awk '
    {
        duration = $1
        if (duration <= 300) short++
        else if (duration <= 1800) medium++
        else long++
        total++
    }
    END {
        printf "   🟢 Short jobs (≤5min): %d\n", short
        printf "   🟡 Medium jobs (5min-30min): %d\n", medium  
        printf "   🔴 Long jobs (>30min): %d\n", long
    }'
fi

echo ""

# ================================================================
# 5. NODE DISTRIBUTION ANALYSIS
# ================================================================
echo "🏠 NODE DISTRIBUTION ANALYSIS"
echo "============================="

echo "📊 Pods per node:"
NODE_DISTRIBUTION=$(kubectl get pods -n $POD_NAMESPACE -o wide --no-headers 2>/dev/null | awk '{print $7}' | sort | uniq -c | sort -nr)
echo "$NODE_DISTRIBUTION"

TOTAL_NODES=$(kubectl get nodes --no-headers 2>/dev/null | wc -l)
NODES_WITH_PODS=$(echo "$NODE_DISTRIBUTION" | wc -l)
EMPTY_NODES=$((TOTAL_NODES - NODES_WITH_PODS))
PODS_PER_NODE=$(calc_percentage $TOTAL_PODS $TOTAL_NODES)

echo ""
echo "📈 Distribution Metrics:"
echo "   Total cluster nodes: $TOTAL_NODES"
echo "   Nodes with pods: $NODES_WITH_PODS"
echo "   Empty nodes: $EMPTY_NODES"
echo "   Average pods per node: $PODS_PER_NODE"

# Check distribution quality
MAX_PODS_ON_NODE=$(echo "$NODE_DISTRIBUTION" | head -1 | awk '{print $1}')
MIN_PODS_ON_NODE=$(echo "$NODE_DISTRIBUTION" | tail -1 | awk '{print $1}')
DISTRIBUTION_RATIO=$((MAX_PODS_ON_NODE - MIN_PODS_ON_NODE))

if [ $DISTRIBUTION_RATIO -le 2 ]; then
    echo "✅ GOOD: Even pod distribution across nodes"
elif [ $DISTRIBUTION_RATIO -le 5 ]; then
    echo "⚠️  FAIR: Moderate pod distribution"
else
    echo "❌ POOR: Uneven pod distribution (max: $MAX_PODS_ON_NODE, min: $MIN_PODS_ON_NODE)"
fi

echo ""

# ================================================================
# 6. SCHEDULING EVENTS ANALYSIS
# ================================================================
echo "📅 SCHEDULING EVENTS ANALYSIS"
echo "============================="

# Recent successful scheduling
RECENT_SCHEDULED=$(kubectl get events -n $POD_NAMESPACE --field-selector reason=Scheduled --no-headers 2>/dev/null | wc -l)
echo "✅ Recent successful scheduling events: $RECENT_SCHEDULED"

if [ $RECENT_SCHEDULED -gt 0 ]; then
    echo "   Last 5 successful schedules:"
    kubectl get events -n $POD_NAMESPACE --field-selector reason=Scheduled --sort-by='.firstTimestamp' 2>/dev/null | tail -5 | awk '{print "   " $1 " " $4 " " $5 " " $6}'
fi

echo ""

# Failed scheduling events
FAILED_EVENTS=$(kubectl get events -n $POD_NAMESPACE --field-selector reason=FailedScheduling --no-headers 2>/dev/null | wc -l)

if [ $FAILED_EVENTS -gt 0 ]; then
    echo "❌ Failed scheduling events: $FAILED_EVENTS"
    echo "   Recent failures:"
    kubectl get events -n $POD_NAMESPACE --field-selector reason=FailedScheduling --sort-by='.firstTimestamp' 2>/dev/null | tail -3 | awk '{print "   " $1 " " $6 " " $7 " " $8}'
else
    echo "✅ No failed scheduling events"
fi

echo ""

# ================================================================
# 7. BIN-PACKING EFFECTIVENESS (if using Chronos)
# ================================================================
if [ $CHRONOS_PODS -gt 0 ] && [ $WITH_ANNOTATION -gt 0 ]; then
    echo "📊 BIN-PACKING EFFECTIVENESS"
    echo "==========================="
    
    # Analyze scheduling patterns by duration and node
    kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '
    .items[] | 
    select(.spec.schedulerName == "'$SCHEDULER_NAME'") |
    select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) |
    [.spec.nodeName, .metadata.annotations["scheduling.workload.io/expected-duration-seconds"]] | 
    @tsv' | awk -F'\t' '
    {
        node = $1
        duration = $2
        
        if (duration <= 300) {
            short[node]++
            total_short++
        } else if (duration <= 1800) {
            medium[node]++
            total_medium++
        } else {
            long[node]++
            total_long++
        }
        nodes[node] = 1
    }
    END {
        print "📊 Job distribution by node (bin-packing analysis):"
        printf "%-25s %8s %10s %8s %8s\n", "NODE", "SHORT", "MEDIUM", "LONG", "TOTAL"
        print "────────────────────────────────────────────────────────────"
        
        consolidation_score = 0
        node_count = 0
        
        for (node in nodes) {
            node_total = short[node] + medium[node] + long[node]
            printf "%-25s %8d %10d %8d %8d\n", node, short[node], medium[node], long[node], node_total
            
            # Calculate consolidation score (higher is better)
            job_types = (short[node] > 0) + (medium[node] > 0) + (long[node] > 0)
            if (job_types > 1) consolidation_score++
            node_count++
        }
        
        print ""
        consolidation_pct = (consolidation_score * 100) / node_count
        printf "📈 Consolidation effectiveness: %.1f%% (%d/%d nodes have mixed workloads)\n", consolidation_pct, consolidation_score, node_count
        
        if (consolidation_pct > 60) {
            print "✅ EXCELLENT: Good bin-packing consolidation"
        } else if (consolidation_pct > 30) {
            print "⚠️  FAIR: Moderate consolidation"
        } else {
            print "❌ POOR: Limited consolidation (jobs not well bin-packed)"
        }
    }'
    
    echo ""
fi

# ================================================================
# 8. PERFORMANCE ANALYSIS
# ================================================================
echo "⚡ PERFORMANCE ANALYSIS"
echo "======================"

# Scheduler scoring activity
if [ ! -z "$SCHEDULER_NAME_POD" ]; then
    SCORING_ACTIVITY=$(kubectl logs -n $SCHEDULER_NAMESPACE $SCHEDULER_NAME_POD --since=1h 2>/dev/null | grep -c "Score.*optimized")
    echo "�� Chronos scoring operations (last hour): $SCORING_ACTIVITY"
    
    if [ $SCORING_ACTIVITY -gt 0 ]; then
        echo "✅ Scheduler actively processing workloads"
    elif [ $TOTAL_PODS -gt 0 ]; then
        echo "⚠️  No recent scoring activity (pods may be stable)"
    fi
fi

# Check for any obvious performance issues
if [ $PENDING -gt 5 ]; then
    echo "⚠️  Performance concern: $PENDING pods pending (check resource constraints)"
fi

if [ $FAILED -gt 0 ]; then
    echo "❌ Performance issue: $FAILED pods failed"
fi

echo ""

# ================================================================
# 9. OVERALL ASSESSMENT & RECOMMENDATIONS
# ================================================================
echo "🎯 OVERALL ASSESSMENT & RECOMMENDATIONS"
echo "======================================"

SCORE=0
MAX_SCORE=10

# Scoring system
echo "📋 Health Check Results:"

# 1. Scheduler running
if [ "$SCHEDULER_STATUS" = "Running" ]; then
    echo "   ✅ Scheduler Health: GOOD"
    SCORE=$((SCORE + 2))
else
    echo "   ❌ Scheduler Health: FAILED"
fi

# 2. Pod success rate
if [ $SUCCESS_RATE -ge 95 ]; then
    echo "   ✅ Scheduling Success: EXCELLENT ($SUCCESS_RATE%)"
    SCORE=$((SCORE + 2))
elif [ $SUCCESS_RATE -ge 80 ]; then
    echo "   ⚠️  Scheduling Success: GOOD ($SUCCESS_RATE%)"
    SCORE=$((SCORE + 1))
else
    echo "   ❌ Scheduling Success: POOR ($SUCCESS_RATE%)"
fi

# 3. Chronos adoption
if [ $CHRONOS_PCT -ge 90 ]; then
    echo "   ✅ Chronos Adoption: EXCELLENT ($CHRONOS_PCT%)"
    SCORE=$((SCORE + 2))
elif [ $CHRONOS_PCT -ge 50 ]; then
    echo "   ⚠️  Chronos Adoption: PARTIAL ($CHRONOS_PCT%)"
    SCORE=$((SCORE + 1))
else
    echo "   ❌ Chronos Adoption: POOR ($CHRONOS_PCT%)"
fi

# 4. Annotation usage
if [ $ANNOTATION_PCT -ge 90 ]; then
    echo "   ✅ Annotation Usage: EXCELLENT ($ANNOTATION_PCT%)"
    SCORE=$((SCORE + 2))
elif [ $ANNOTATION_PCT -ge 50 ]; then
    echo "   ⚠️  Annotation Usage: PARTIAL ($ANNOTATION_PCT%)"
    SCORE=$((SCORE + 1))
else
    echo "   ❌ Annotation Usage: POOR ($ANNOTATION_PCT%)"
fi

# 5. No failures
if [ $FAILED_EVENTS -eq 0 ]; then
    echo "   ✅ Scheduling Failures: NONE"
    SCORE=$((SCORE + 2))
elif [ $FAILED_EVENTS -le 2 ]; then
    echo "   ⚠️  Scheduling Failures: FEW ($FAILED_EVENTS)"
    SCORE=$((SCORE + 1))
else
    echo "   ❌ Scheduling Failures: MANY ($FAILED_EVENTS)"
fi

SCORE_PCT=$(calc_percentage $SCORE $MAX_SCORE)

echo ""
echo "🏆 OVERALL SCORE: $SCORE/$MAX_SCORE ($SCORE_PCT%)"

if [ $SCORE_PCT -ge 80 ]; then
    echo "🎉 STATUS: EXCELLENT - Chronos scheduler is working optimally!"
elif [ $SCORE_PCT -ge 60 ]; then
    echo "👍 STATUS: GOOD - Chronos scheduler is working well with minor issues"
elif [ $SCORE_PCT -ge 40 ]; then
    echo "⚠️  STATUS: FAIR - Chronos scheduler needs attention"
else
    echo "❌ STATUS: POOR - Chronos scheduler requires immediate fixes"
fi

echo ""
echo "💡 RECOMMENDATIONS:"

# Provide specific recommendations
if [ $CHRONOS_PODS -lt $TOTAL_PODS ]; then
    echo "   🔧 Add 'schedulerName: $SCHEDULER_NAME' to pod specs"
fi

if [ $WITHOUT_ANNOTATION -gt 0 ]; then
    echo "   🔧 Add 'scheduling.workload.io/expected-duration-seconds' annotation to $WITHOUT_ANNOTATION pods"
fi

if [ $PENDING -gt 0 ]; then
    echo "   �� Investigate $PENDING pending pods - check resource constraints"
fi

if [ $FAILED -gt 0 ]; then
    echo "   🔧 Investigate $FAILED failed pods - check node resources and taints"
fi

if [ $FAILED_EVENTS -gt 0 ]; then
    echo "   🔧 Review failed scheduling events for resource or constraint issues"
fi

if [ $EMPTY_NODES -eq 0 ] && [ $TOTAL_PODS -gt $TOTAL_NODES ]; then
    echo "   ℹ️  Consider if empty node penalty is working as expected"
fi

echo ""
echo "📄 Analysis completed at: $(date '+%Y-%m-%d %H:%M:%S')"
echo "📁 Pod namespace: $POD_NAMESPACE"
echo "📁 Scheduler namespace: $SCHEDULER_NAMESPACE"
echo "🎯 Scheduler: $SCHEDULER_NAME"
echo "✅ Analysis complete!"
