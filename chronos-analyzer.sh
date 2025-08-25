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
    echo "⚠️  WARNING: Chronos scheduler pod not found!"
    echo "   Analysis will continue for general pod scheduling patterns"
    echo "   To get Chronos-specific metrics, deploy the scheduler in '$SCHEDULER_NAMESPACE' namespace"
    echo ""
    SCHEDULER_STATUS="Not Found"
    SCHEDULER_NAME_POD="N/A"
else

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

fi  # End of scheduler check

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

# Pod status counts (for entire namespace overview)
RUNNING=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l)
PENDING=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Pending --no-headers 2>/dev/null | wc -l)
FAILED=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Failed --no-headers 2>/dev/null | wc -l)
SUCCEEDED=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Succeeded --no-headers 2>/dev/null | wc -l)

# Calculate percentages for namespace overview
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

# Calculate Chronos-specific pod status (for accurate scoring)
if [ $CHRONOS_PODS -gt 0 ]; then
    CHRONOS_RUNNING=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.spec.schedulerName=="'$SCHEDULER_NAME'") | select(.status.phase=="Running") | .metadata.name' | wc -l)
    CHRONOS_PENDING=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.spec.schedulerName=="'$SCHEDULER_NAME'") | select(.status.phase=="Pending") | .metadata.name' | wc -l)
    CHRONOS_FAILED=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.spec.schedulerName=="'$SCHEDULER_NAME'") | select(.status.phase=="Failed") | .metadata.name' | wc -l)
    
    CHRONOS_SUCCESS_RATE=$(calc_percentage $CHRONOS_RUNNING $CHRONOS_PODS)
    
    echo ""
    echo "📊 Chronos Scheduler Pod Status:"
    echo "   🟢 Running: $CHRONOS_RUNNING/$CHRONOS_PODS ($CHRONOS_SUCCESS_RATE%)"
    echo "   🟡 Pending: $CHRONOS_PENDING"
    echo "   ❌ Failed: $CHRONOS_FAILED"
else
    CHRONOS_SUCCESS_RATE=0
fi

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

# Annotation usage for all pods (namespace overview)
WITH_ANNOTATION_ALL=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) | .metadata.name' | wc -l)
WITHOUT_ANNOTATION_ALL=$((TOTAL_PODS - WITH_ANNOTATION_ALL))

# Annotation usage for Chronos-scheduled pods only (for accurate scoring)
if [ $CHRONOS_PODS -gt 0 ]; then
    CHRONOS_WITH_ANNOTATION=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.spec.schedulerName=="'$SCHEDULER_NAME'") | select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) | .metadata.name' | wc -l)
    CHRONOS_WITHOUT_ANNOTATION=$((CHRONOS_PODS - CHRONOS_WITH_ANNOTATION))
    CHRONOS_ANNOTATION_PCT=$(calc_percentage $CHRONOS_WITH_ANNOTATION $CHRONOS_PODS)
else
    CHRONOS_WITH_ANNOTATION=0
    CHRONOS_WITHOUT_ANNOTATION=0
    CHRONOS_ANNOTATION_PCT=0
fi

ANNOTATION_PCT=$(calc_percentage $WITH_ANNOTATION_ALL $TOTAL_PODS)

echo "📊 Duration Annotation Usage (All Pods):"
echo "   ✅ With annotation: $WITH_ANNOTATION_ALL ($ANNOTATION_PCT%)"
echo "   ❌ Without annotation: $WITHOUT_ANNOTATION_ALL"

if [ $CHRONOS_PODS -gt 0 ]; then
    echo ""
    echo "📊 Chronos Pods Annotation Usage:"
    echo "   ✅ With annotation: $CHRONOS_WITH_ANNOTATION/$CHRONOS_PODS ($CHRONOS_ANNOTATION_PCT%)"
    echo "   ❌ Without annotation: $CHRONOS_WITHOUT_ANNOTATION"
fi

if [ $WITH_ANNOTATION_ALL -eq $TOTAL_PODS ]; then
    echo "✅ EXCELLENT: All pods have duration annotations"
elif [ $WITH_ANNOTATION_ALL -gt 0 ]; then
    echo "⚠️  WARNING: $WITHOUT_ANNOTATION_ALL pods lack duration annotations"
    echo "   → These pods fall back to NodeResourcesFit scoring only"
else
    echo "❌ CRITICAL: No pods have duration annotations!"
    echo "   → Chronos time-based optimization not working!"
fi

# Show annotation values distribution if any exist
if [ $WITH_ANNOTATION_ALL -gt 0 ]; then
    echo ""
    echo "📊 Duration Distribution:"
    kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '
    .items[] | 
    select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) | 
    .metadata.annotations["scheduling.workload.io/expected-duration-seconds"]' | sort -n | awk '
    {
        duration = $1
        if (duration < 300) under5++
        else if (duration < 600) between5_10++
        else if (duration < 900) between10_15++
        else if (duration < 1200) between15_20++
        else if (duration < 1500) between20_25++
        else if (duration <= 1800) between25_30++
        else over30++
        total++
    }
    END {
        printf "   🟢 <5min: %d\n", under5
        printf "   🟡 5-10min: %d\n", between5_10
        printf "   🟠 10-15min: %d\n", between10_15
        printf "   🔶 15-20min: %d\n", between15_20
        printf "   🔸 20-25min: %d\n", between20_25
        printf "   🔴 25-30min: %d\n", between25_30
        printf "   ⚫ >30min: %d\n", over30
    }'
fi

echo ""

# ================================================================
# 5. NODE DISTRIBUTION ANALYSIS
# ================================================================
echo "🏠 NODE DISTRIBUTION ANALYSIS"
echo "============================="

echo "📊 All pods per node (entire namespace):"
NODE_DISTRIBUTION=$(kubectl get pods -n $POD_NAMESPACE -o wide --no-headers 2>/dev/null | awk '{print $7}' | sort | uniq -c | sort -nr)
echo "$NODE_DISTRIBUTION"

# Show pods with duration annotations per node
if [ $WITH_ANNOTATION_ALL -gt 0 ]; then
    echo ""
    echo "🎯 Pods with duration annotations per node:"
    ANNOTATED_DISTRIBUTION=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '.items[] | select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) | .spec.nodeName' | sort | uniq -c | sort -nr)
    if [ ! -z "$ANNOTATED_DISTRIBUTION" ]; then
        echo "$ANNOTATED_DISTRIBUTION"
        
        # Calculate distribution metrics for annotated pods
        ANNOTATED_NODES_WITH_PODS=$(echo "$ANNOTATED_DISTRIBUTION" | wc -l)
        MAX_ANNOTATED_ON_NODE=$(echo "$ANNOTATED_DISTRIBUTION" | head -1 | awk '{print $1}')
        MIN_ANNOTATED_ON_NODE=$(echo "$ANNOTATED_DISTRIBUTION" | tail -1 | awk '{print $1}')
        ANNOTATED_DISTRIBUTION_RATIO=$((MAX_ANNOTATED_ON_NODE - MIN_ANNOTATED_ON_NODE))
        
        echo ""
        echo "📈 Annotated Pod Distribution Quality:"
        if [ $ANNOTATED_DISTRIBUTION_RATIO -le 2 ]; then
            echo "✅ EXCELLENT: Even distribution of annotated pods (max: $MAX_ANNOTATED_ON_NODE, min: $MIN_ANNOTATED_ON_NODE)"
        elif [ $ANNOTATED_DISTRIBUTION_RATIO -le 5 ]; then
            echo "⚠️  GOOD: Moderate distribution of annotated pods (max: $MAX_ANNOTATED_ON_NODE, min: $MIN_ANNOTATED_ON_NODE)"
        else
            echo "❌ POOR: Uneven distribution of annotated pods (max: $MAX_ANNOTATED_ON_NODE, min: $MIN_ANNOTATED_ON_NODE)"
        fi
    else
        echo "   No pods with duration annotations found"
    fi
fi

TOTAL_NODES=$(kubectl get nodes --no-headers 2>/dev/null | wc -l)
NODES_WITH_PODS=$(echo "$NODE_DISTRIBUTION" | wc -l)
EMPTY_NODES=$((TOTAL_NODES - NODES_WITH_PODS))
PODS_PER_NODE=$(calc_percentage $TOTAL_PODS $TOTAL_NODES)

echo ""
echo "📈 Overall Distribution Metrics (all pods):"
echo "   Total cluster nodes: $TOTAL_NODES"
echo "   Nodes with pods: $NODES_WITH_PODS"
echo "   Empty nodes: $EMPTY_NODES"
echo "   Average pods per node: $PODS_PER_NODE"

# Check overall distribution quality
MAX_PODS_ON_NODE=$(echo "$NODE_DISTRIBUTION" | head -1 | awk '{print $1}')
MIN_PODS_ON_NODE=$(echo "$NODE_DISTRIBUTION" | tail -1 | awk '{print $1}')
DISTRIBUTION_RATIO=$((MAX_PODS_ON_NODE - MIN_PODS_ON_NODE))

echo "📊 Overall Distribution Quality:"
if [ $DISTRIBUTION_RATIO -le 2 ]; then
    echo "✅ EXCELLENT: Even overall pod distribution (max: $MAX_PODS_ON_NODE, min: $MIN_PODS_ON_NODE)"
elif [ $DISTRIBUTION_RATIO -le 5 ]; then
    echo "⚠️  GOOD: Moderate overall pod distribution (max: $MAX_PODS_ON_NODE, min: $MIN_PODS_ON_NODE)"
else
    echo "❌ POOR: Uneven overall pod distribution (max: $MAX_PODS_ON_NODE, min: $MIN_PODS_ON_NODE)"
fi

echo ""

# ================================================================
# 6. CHRONOS SCHEDULING PERFORMANCE ANALYSIS
# ================================================================
if [ $CHRONOS_PODS -gt 0 ]; then
    echo "⚡ CHRONOS SCHEDULING PERFORMANCE"
    echo "================================"

    # Analyze Chronos vs Default scheduler performance
    EVENTS_OUTPUT=$(kubectl get events -n $POD_NAMESPACE --field-selector reason=Scheduled --no-headers 2>/dev/null || echo "")
    CHRONOS_SCHEDULED=$(echo "$EVENTS_OUTPUT" | grep -c "chronos-kubernetes-scheduler" 2>/dev/null || echo "0")
    DEFAULT_SCHEDULED=$(echo "$EVENTS_OUTPUT" | grep -c "default-scheduler" 2>/dev/null || echo "0")
    # Clean up any whitespace and ensure we have valid numbers
    CHRONOS_SCHEDULED=$(echo "$CHRONOS_SCHEDULED" | tr -d ' \n\r\t')
    DEFAULT_SCHEDULED=$(echo "$DEFAULT_SCHEDULED" | tr -d ' \n\r\t')
    CHRONOS_SCHEDULED=${CHRONOS_SCHEDULED:-0}
    DEFAULT_SCHEDULED=${DEFAULT_SCHEDULED:-0}
    TOTAL_SCHEDULED=$((CHRONOS_SCHEDULED + DEFAULT_SCHEDULED))

    if [ $TOTAL_SCHEDULED -gt 0 ]; then
        CHRONOS_SCHED_PCT=$(calc_percentage $CHRONOS_SCHEDULED $TOTAL_SCHEDULED)
        echo "📊 Recent scheduling activity:"
        echo "   🎯 Chronos scheduler: $CHRONOS_SCHEDULED events ($CHRONOS_SCHED_PCT%)"
        echo "   🔷 Default scheduler: $DEFAULT_SCHEDULED events"
        
        if [ $(echo "$CHRONOS_SCHED_PCT >= 80" | bc -l) -eq 1 ]; then
            echo "✅ EXCELLENT: Chronos handling most scheduling"
        elif [ $(echo "$CHRONOS_SCHED_PCT >= 50" | bc -l) -eq 1 ]; then
            echo "⚠️  MODERATE: Mixed scheduler usage"
        else
            echo "❌ LOW: Most pods using default scheduler"
        fi
    else
        echo "ℹ️  No recent scheduling events found"
    fi

    echo ""

    # Show recent Chronos-scheduled pods (removing misleading "speed" metric)
    RECENT_CHRONOS_PODS=$(kubectl get pods -n $POD_NAMESPACE --sort-by='.metadata.creationTimestamp' --no-headers 2>/dev/null | tail -10 | while read line; do
        POD_NAME=$(echo "$line" | awk '{print $1}')
        POD_STATUS=$(echo "$line" | awk '{print $3}')
        
        if [ "$POD_STATUS" = "Running" ] || [ "$POD_STATUS" = "Completed" ]; then
            # Get scheduler used for this pod
            SCHEDULER_USED=$(kubectl get pod $POD_NAME -n $POD_NAMESPACE -o jsonpath='{.spec.schedulerName}' 2>/dev/null || echo "default")
            
            if [ "$SCHEDULER_USED" = "$SCHEDULER_NAME" ]; then
                echo "$line"
            fi
        fi
    done)
    
    if [ ! -z "$RECENT_CHRONOS_PODS" ]; then
        CHRONOS_RUNNING_COUNT=$(echo "$RECENT_CHRONOS_PODS" | wc -l)
        echo "✅ Recent Chronos pods successfully running: $CHRONOS_RUNNING_COUNT/10"
        echo "   (Note: Actual scheduling latency analysis requires event correlation)"
    else
        echo "ℹ️  No recently created Chronos pods found"
    fi
else
    echo "⚠️  SCHEDULING ANALYSIS SKIPPED"
    echo "==============================="
    echo "   No Chronos-scheduled pods found for performance analysis"
fi

echo ""

# Analyze current scheduling issues vs historical events
RECENT_FAILED_EVENTS=$(kubectl get events -n $POD_NAMESPACE --field-selector reason=FailedScheduling --no-headers 2>/dev/null | awk 'NR<=50' | wc -l)
TOTAL_FAILED_EVENTS=$(kubectl get events -n $POD_NAMESPACE --field-selector reason=FailedScheduling --no-headers 2>/dev/null | wc -l)

# Focus on currently stuck pods (pending for >5 minutes)
STUCK_PODS=$(kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Pending --no-headers 2>/dev/null | awk '
BEGIN { stuck_count = 0 }
{
    # Extract age column (usually column 5, but can vary)
    age = $(NF)
    
    # Convert age to minutes for comparison
    if (age ~ /[0-9]+m/) {
        minutes = int(age)
        if (minutes >= 5) stuck_count++
    } else if (age ~ /[0-9]+h/) {
        stuck_count++  # Any hours is definitely stuck
    } else if (age ~ /[0-9]+d/) {
        stuck_count++  # Any days is definitely stuck
    }
}
END { print stuck_count }')

echo "📊 Scheduling Issues Analysis:"
echo "   🕒 Currently stuck pods (>5min pending): $STUCK_PODS"
echo "   ⚠️  Recent failed events (last 50): $RECENT_FAILED_EVENTS"
echo "   📜 Total historical failed events: $TOTAL_FAILED_EVENTS"

if [ $STUCK_PODS -gt 0 ]; then
    echo ""
    echo "❌ CRITICAL: $STUCK_PODS pods are stuck in pending state!"
    echo "   Pods pending >5 minutes:"
    kubectl get pods -n $POD_NAMESPACE --field-selector=status.phase=Pending --no-headers 2>/dev/null | awk '
    {
        age = $(NF)
        if (age ~ /[0-9]+m/ && int(age) >= 5) print "   " $1 " (age: " age ")"
        else if (age ~ /[0-9]+[hd]/) print "   " $1 " (age: " age ")"
    }'
    
    echo ""
    echo "   Recent scheduling failure reasons:"
    kubectl get events -n $POD_NAMESPACE --field-selector reason=FailedScheduling --sort-by='.firstTimestamp' 2>/dev/null | tail -3 | awk '{print "   " $1 ": " $NF}'
elif [ $RECENT_FAILED_EVENTS -gt 0 ]; then
    echo "⚠️  Some recent scheduling retries occurred, but no pods currently stuck"
else
    echo "✅ No current scheduling issues"
fi

# Set FAILED_EVENTS to stuck pods count for assessment (not historical events)
FAILED_EVENTS=$STUCK_PODS

echo ""

# ================================================================
# 7. BIN-PACKING EFFECTIVENESS (if using Chronos)
# ================================================================
if [ $CHRONOS_PODS -gt 0 ] && [ $WITH_ANNOTATION_ALL -gt 0 ]; then
    echo "📊 BIN-PACKING EFFECTIVENESS"
    echo "==========================="
    
    # Analyze scheduling patterns by duration and node (ALL pods with duration annotations)
    kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '
    .items[] | 
    select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) |
    [.spec.nodeName, .metadata.annotations["scheduling.workload.io/expected-duration-seconds"]] | 
    @tsv' | awk -F'\t' '
    {
        node = $1
        duration = $2
        
        if (duration < 600) {
            short[node]++  # <10min (combines <5min and 5-10min)
            total_short++
        } else if (duration < 1200) {
            medium[node]++  # 10-20min (combines 10-15min and 15-20min)
            total_medium++
        } else if (duration <= 1800) {
            long[node]++   # 20-30min (combines 20-25min and 25-30min)
            total_long++
        } else {
            vlong[node]++  # >30min
            total_vlong++
        }
        nodes[node] = 1
    }
    END {
        # Calculate dynamic node column width based on actual node names
        max_node_length = length("NODE")  # Start with header width
        for (node in nodes) {
            if (length(node) > max_node_length) {
                max_node_length = length(node)
            }
        }
        # Add 2 characters padding for better readability
        node_width = max_node_length + 2
        
        print "📊 Job distribution by node (bin-packing analysis):"
        printf "%-*s %7s %7s %7s %7s %7s\n", node_width, "NODE", "<10min", "10-20m", "20-30m", ">30min", "TOTAL"
        
        # Create separator line with dynamic width
        separator = ""
        for (i = 1; i <= node_width; i++) separator = separator "─"
        printf "%-*s %7s %7s %7s %7s %7s\n", node_width, separator, "───────", "───────", "───────", "───────", "───────"
        
        consolidation_score = 0
        node_count = 0
        
        for (node in nodes) {
            node_total = short[node] + medium[node] + long[node] + vlong[node]
            printf "%-*s %7d %7d %7d %7d %7d\n", node_width, node, short[node], medium[node], long[node], vlong[node], node_total
            
            # Calculate consolidation score (higher is better)
            job_types = (short[node] > 0) + (medium[node] > 0) + (long[node] > 0) + (vlong[node] > 0)
            if (job_types > 1) consolidation_score++
            node_count++
        }
        
        print ""
        consolidation_pct = (node_count > 0) ? (consolidation_score * 100) / node_count : 0
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
    
    # ================================================================
    # NODE EXPECTED DURATION PERCENTILES
    # ================================================================
    echo "⏱️  NODE EXPECTED DURATION PERCENTILES"
    echo "======================================"
    
    # Get expected durations for all pods with duration annotations
    # Note: Shows ALL pods with duration annotations, regardless of scheduler
    COMPLETION_DATA=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '
    .items[] | 
    select(.spec.nodeName != null) |
    select(.metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null) |
    {
        node: .spec.nodeName,
        expectedDuration: .metadata.annotations["scheduling.workload.io/expected-duration-seconds"]
    } |
    [.node, .expectedDuration] | 
    @tsv' 2>/dev/null)
    
    # Get total pods per node and annotation statistics
    NODE_STATS=$(kubectl get pods -n $POD_NAMESPACE -o json 2>/dev/null | jq -r '
    .items[] |
    select(.spec.nodeName != null) |
    {
        node: .spec.nodeName,
        hasAnnotation: (if .metadata.annotations["scheduling.workload.io/expected-duration-seconds"] != null then "1" else "0" end)
    } |
    [.node, .hasAnnotation] |
    @tsv' 2>/dev/null)
    
    if [ ! -z "$COMPLETION_DATA" ] && [ "$COMPLETION_DATA" != "" ]; then
        # Combine completion data and node statistics for processing
        {
            echo "$COMPLETION_DATA"
            echo "---NODE_STATS---"
            echo "$NODE_STATS"
        } | awk -F'\t' '
        function format_duration(seconds) {
            if (seconds < 60) return seconds "s"
            if (seconds < 3600) return int(seconds/60) "m" int(seconds%60) "s"
            return int(seconds/3600) "h" int((seconds%3600)/60) "m"
        }
        
        /^---NODE_STATS---$/ {
            processing_stats = 1
            next
        }
        
        processing_stats {
            # Process node statistics
            node = $1
            has_annotation = $2
            
            total_pods[node]++
            if (has_annotation == "1") {
                annotated_pods[node]++
            }
            all_nodes[node] = 1
            next
        }
        
        !processing_stats {
            # Process expected durations for pods with annotations
            node = $1
            expected_duration = int($2)
            
            if (expected_duration <= 0) {
                next
            }
            
            # Store expected durations for each node
            if (completion_nodes[node] == "") {
                expected_durations[node] = expected_duration
                completion_counts[node] = 1
            } else {
                expected_durations[node] = expected_durations[node] "," expected_duration
                completion_counts[node]++
            }
            completion_nodes[node] = 1
            all_nodes[node] = 1
        }
        
        END {
            if (length(all_nodes) == 0) {
                print "   No pods found on any nodes"
                next
            }
            
            # Calculate dynamic node column width
            max_node_length = length("NODE")
            for (node in all_nodes) {
                if (length(node) > max_node_length) {
                    max_node_length = length(node)
                }
            }
            node_width = max_node_length + 2
            
            print "📊 Expected durations by node (annotated pods):"
            printf "%-*s %5s %5s %5s %4s %8s %8s %8s %8s %8s\n", node_width, "NODE", "TOTAL", "ANNOT", "WITH", "%", "MIN", "P50", "P75", "P90", "MAX"
            
            # Create separator line
            separator = ""
            for (i = 1; i <= node_width; i++) separator = separator "─"
            printf "%-*s %5s %5s %5s %4s %8s %8s %8s %8s %8s\n", node_width, separator, "─────", "─────", "─────", "────", "────────", "────────", "────────", "────────", "────────"
            
            # Process each node and collect performance stats
            total_p50 = 0
            total_p90 = 0
            node_perf_count = 0
            
            for (node in all_nodes) {
                # Get node statistics
                total_count = total_pods[node] ? total_pods[node] : 0
                annotated_count = annotated_pods[node] ? annotated_pods[node] : 0
                with_duration_count = completion_counts[node] ? completion_counts[node] : 0
                
                # Calculate annotation percentage
                annotation_pct = (total_count > 0) ? int((annotated_count * 100) / total_count) : 0
                
                # Process expected durations for annotated pods if available
                if (with_duration_count > 0 && expected_durations[node] != "") {
                    # Split expected durations string into array and sort
                    split(expected_durations[node], node_durations, ",")
                    
                    # Sort expected durations for percentile calculations
                    for (i = 1; i <= with_duration_count - 1; i++) {
                        for (j = i + 1; j <= with_duration_count; j++) {
                            if (int(node_durations[i]) > int(node_durations[j])) {
                                temp = node_durations[i]
                                node_durations[i] = node_durations[j]
                                node_durations[j] = temp
                            }
                        }
                    }
                    
                    # Calculate percentiles for expected durations
                    min_val = int(node_durations[1])
                    max_val = int(node_durations[with_duration_count])
                    p50_idx = int(with_duration_count * 0.5) + 1
                    p75_idx = int(with_duration_count * 0.75) + 1
                    p90_idx = int(with_duration_count * 0.90) + 1
                    
                    # Handle edge cases for small arrays
                    if (p50_idx < 1) p50_idx = 1
                    if (p75_idx < 1) p75_idx = 1
                    if (p90_idx < 1) p90_idx = 1
                    if (p50_idx > with_duration_count) p50_idx = with_duration_count
                    if (p75_idx > with_duration_count) p75_idx = with_duration_count
                    if (p90_idx > with_duration_count) p90_idx = with_duration_count
                    
                    p50 = int(node_durations[p50_idx])
                    p75 = int(node_durations[p75_idx])
                    p90 = int(node_durations[p90_idx])
                    
                    # Accumulate for cluster-wide stats
                    total_p50 += p50
                    total_p90 += p90
                    node_perf_count++
                    
                    printf "%-*s %5d %5d %5d %3d%% %8s %8s %8s %8s %8s\n", node_width, node, total_count, annotated_count, with_duration_count, annotation_pct, format_duration(min_val), format_duration(p50), format_duration(p75), format_duration(p90), format_duration(max_val)
                } else {
                    # Node has pods but no pods with duration annotations
                    printf "%-*s %5d %5d %5d %3d%% %8s %8s %8s %8s %8s\n", node_width, node, total_count, annotated_count, with_duration_count, annotation_pct, "─", "─", "─", "─", "─"
                }
            }
            
            # Show cluster-wide duration summary
            if (node_perf_count > 0) {
                avg_p50 = total_p50 / node_perf_count
                avg_p90 = total_p90 / node_perf_count
                printf "%-*s %5s %5s %5s %4s %8s %8s %8s %8s %8s\n", node_width, "CLUSTER AVG", "─", "─", "─", "─", "─", format_duration(avg_p50), "─", format_duration(avg_p90), "─"
            }
            
            print ""
            print "📈 Expected Duration Insights:"
            
            # Analyze duration patterns and provide specific insights
            if (node_perf_count > 1) {
                # Find nodes with shortest and longest expected durations
                shortest_p50 = 999999
                longest_p50 = 0
                shortest_node = ""
                longest_node = ""
                
                for (node in all_nodes) {
                    with_duration_count = completion_counts[node] ? completion_counts[node] : 0
                    if (with_duration_count == 0 || expected_durations[node] == "") continue
                    
                    # Calculate p50 expected duration for this node
                    split(expected_durations[node], node_durations, ",")
                    p50_idx = int(with_duration_count * 0.5) + 1
                    if (p50_idx > with_duration_count) p50_idx = with_duration_count
                    if (p50_idx < 1) p50_idx = 1
                    
                    node_p50 = int(node_durations[p50_idx])
                    
                    if (node_p50 < shortest_p50) {
                        shortest_p50 = node_p50
                        shortest_node = node
                    }
                    if (node_p50 > longest_p50) {
                        longest_p50 = node_p50
                        longest_node = node
                    }
                }
                
                if (shortest_node != "" && longest_node != "" && shortest_node != longest_node) {
                    duration_spread = ((longest_p50 - shortest_p50) / shortest_p50) * 100
                    printf "   ⚡ Shortest jobs: %s (%s median)\n", shortest_node, format_duration(shortest_p50)
                    printf "   🕐 Longest jobs: %s (%s median, %.0f%% longer)\n", longest_node, format_duration(longest_p50), duration_spread
                }
                
                if (avg_p50 > 0 && avg_p90 > 0) {
                    variability = ((avg_p90 - avg_p50) / avg_p50) * 100
                    if (variability > 200) {
                        print "   ⚠️  High duration variability across nodes - diverse workloads"
                    } else if (variability > 100) {
                        print "   📊 Moderate duration spread - mixed workload types"
                    } else {
                        print "   ✅ Consistent expected durations across nodes"
                    }
                }
            } else {
                print "   📊 Single node analysis - no duration comparison available"
            }
        
        
        # Add annotation usage insights
        if (length(all_nodes) > 0) {
            total_cluster_pods = 0
            total_cluster_annotated = 0
            total_with_durations = 0
            nodes_with_low_annotation = 0
            
            for (node in all_nodes) {
                node_total = total_pods[node] ? total_pods[node] : 0
                node_annotated = annotated_pods[node] ? annotated_pods[node] : 0
                node_with_durations = completion_counts[node] ? completion_counts[node] : 0
                total_cluster_pods += node_total
                total_cluster_annotated += node_annotated
                total_with_durations += node_with_durations
                
                if (node_total > 0) {
                    node_annotation_pct = (node_annotated * 100) / node_total
                    if (node_annotation_pct < 50) {
                        nodes_with_low_annotation++
                    }
                }
            }
            
            cluster_annotation_pct = (total_cluster_pods > 0) ? (total_cluster_annotated * 100) / total_cluster_pods : 0
            
            printf "   📊 Cluster annotation coverage: %.0f%% (%d/%d pods)\n", cluster_annotation_pct, total_cluster_annotated, total_cluster_pods
            printf "   📝 Pods with duration annotations: %d (used for duration analysis)\n", total_with_durations
            
            if (nodes_with_low_annotation > 0) {
                printf "   ⚠️  %d nodes have <50%% annotation coverage - consider adding duration annotations\n", nodes_with_low_annotation
            }
        }
        
        print "   💡 Use these metrics to understand workload distribution and expected durations"
        }'
    else
        # Even without duration data, show basic node statistics if available
        if [ ! -z "$NODE_STATS" ] && [ "$NODE_STATS" != "" ]; then
            echo "   ⚠️  No pods found with duration annotations"
            echo "   → Showing basic node statistics only"
            echo ""
            
            echo "$NODE_STATS" | awk -F'\t' '
            {
                node = $1
                has_annotation = $2
                
                total_pods[node]++
                if (has_annotation == "1") {
                    annotated_pods[node]++
                }
                all_nodes[node] = 1
            }
            END {
                # Calculate dynamic node column width
                max_node_length = length("NODE")
                for (node in all_nodes) {
                    if (length(node) > max_node_length) {
                        max_node_length = length(node)
                    }
                }
                node_width = max_node_length + 2
                
                print "📊 Pod statistics by node (no expected durations available):"
                printf "%-*s %5s %5s %4s\n", node_width, "NODE", "TOTAL", "ANNOT", "%"
                
                separator = ""
                for (i = 1; i <= node_width; i++) separator = separator "─"
                printf "%-*s %5s %5s %4s\n", node_width, separator, "─────", "─────", "────"
                
                for (node in all_nodes) {
                    total_count = total_pods[node] ? total_pods[node] : 0
                    annotated_count = annotated_pods[node] ? annotated_pods[node] : 0
                    annotation_pct = (total_count > 0) ? int((annotated_count * 100) / total_count) : 0
                    
                    printf "%-*s %5d %5d %3d%%\n", node_width, node, total_count, annotated_count, annotation_pct
                }
            }'
        else
            echo "   ⚠️  No pods found in namespace '$POD_NAMESPACE'"
            echo "   → Deploy some pods with duration annotations to see expected duration analysis"
        fi
    fi
    
    echo ""
fi

# ================================================================
# 8. PERFORMANCE ANALYSIS
# ================================================================
echo "⚡ PERFORMANCE ANALYSIS"
echo "======================"

# Scheduler scoring activity
if [ ! -z "$SCHEDULER_NAME_POD" ] && [ "$SCHEDULER_NAME_POD" != "N/A" ]; then
    SCORING_ACTIVITY=$(kubectl logs -n $SCHEDULER_NAMESPACE $SCHEDULER_NAME_POD --since=1h 2>/dev/null | grep -c "Score.*optimized")
    echo "⚡ Chronos scoring operations (last hour): $SCORING_ACTIVITY"
    
    if [ $SCORING_ACTIVITY -gt 0 ]; then
        echo "✅ Scheduler actively processing workloads"
    elif [ $TOTAL_PODS -gt 0 ]; then
        echo "⚠️  No recent scoring activity (pods may be stable)"
    fi
else
    echo "⚠️  Chronos scheduler not deployed - showing general pod analysis"
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
# 8. OVERALL ASSESSMENT & RECOMMENDATIONS
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

# 2. Chronos pod success rate
if [ $CHRONOS_PODS -gt 0 ]; then
    if [ $(echo "$CHRONOS_SUCCESS_RATE >= 95" | bc -l) -eq 1 ]; then
        echo "   ✅ Chronos Scheduling Success: EXCELLENT ($CHRONOS_SUCCESS_RATE%)"
        SCORE=$((SCORE + 2))
    elif [ $(echo "$CHRONOS_SUCCESS_RATE >= 80" | bc -l) -eq 1 ]; then
        echo "   ⚠️  Chronos Scheduling Success: GOOD ($CHRONOS_SUCCESS_RATE%)"
        SCORE=$((SCORE + 1))
    else
        echo "   ❌ Chronos Scheduling Success: POOR ($CHRONOS_SUCCESS_RATE%)"
    fi
else
    echo "   ❓ Chronos Scheduling Success: N/A (no Chronos pods found)"
fi

# 3. Chronos adoption
if [ $(echo "$CHRONOS_PCT >= 90" | bc -l) -eq 1 ]; then
    echo "   ✅ Chronos Adoption: EXCELLENT ($CHRONOS_PCT%)"
    SCORE=$((SCORE + 2))
elif [ $(echo "$CHRONOS_PCT >= 50" | bc -l) -eq 1 ]; then
    echo "   ⚠️  Chronos Adoption: PARTIAL ($CHRONOS_PCT%)"
    SCORE=$((SCORE + 1))
else
    echo "   ❌ Chronos Adoption: POOR ($CHRONOS_PCT%)"
fi

# 4. Chronos annotation usage
if [ $CHRONOS_PODS -gt 0 ]; then
    if [ $(echo "$CHRONOS_ANNOTATION_PCT >= 90" | bc -l) -eq 1 ]; then
        echo "   ✅ Chronos Annotation Usage: EXCELLENT ($CHRONOS_ANNOTATION_PCT%)"
        SCORE=$((SCORE + 2))
    elif [ $(echo "$CHRONOS_ANNOTATION_PCT >= 50" | bc -l) -eq 1 ]; then
        echo "   ⚠️  Chronos Annotation Usage: PARTIAL ($CHRONOS_ANNOTATION_PCT%)"
        SCORE=$((SCORE + 1))
    else
        echo "   ❌ Chronos Annotation Usage: POOR ($CHRONOS_ANNOTATION_PCT%)"
    fi
else
    echo "   ❓ Chronos Annotation Usage: N/A (no Chronos pods found)"
fi

# 5. Currently stuck pods (not historical events)
if [ $FAILED_EVENTS -eq 0 ]; then
    echo "   ✅ Stuck Pods: NONE (no pods pending >5min)"
    SCORE=$((SCORE + 2))
elif [ $FAILED_EVENTS -le 2 ]; then
    echo "   ⚠️  Stuck Pods: FEW ($FAILED_EVENTS pods pending >5min)"
    SCORE=$((SCORE + 1))
else
    echo "   ❌ Stuck Pods: MANY ($FAILED_EVENTS pods pending >5min)"
fi

SCORE_PCT=$(calc_percentage $SCORE $MAX_SCORE)

echo ""
echo "🏆 OVERALL SCORE: $SCORE/$MAX_SCORE ($SCORE_PCT%)"

if [ $(echo "$SCORE_PCT >= 80" | bc -l) -eq 1 ]; then
    echo "🎉 STATUS: EXCELLENT - Chronos scheduler is working optimally!"
elif [ $(echo "$SCORE_PCT >= 60" | bc -l) -eq 1 ]; then
    echo "👍 STATUS: GOOD - Chronos scheduler is working well with minor issues"
elif [ $(echo "$SCORE_PCT >= 40" | bc -l) -eq 1 ]; then
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

if [ $CHRONOS_WITHOUT_ANNOTATION -gt 0 ]; then
    echo "   🔧 Add 'scheduling.workload.io/expected-duration-seconds' annotation to $CHRONOS_WITHOUT_ANNOTATION Chronos-scheduled pods"
fi

if [ $PENDING -gt 0 ]; then
    echo "   🔍 Investigate $PENDING pending pods - check resource constraints"
fi

if [ $FAILED -gt 0 ]; then
    echo "   🔧 Investigate $FAILED failed pods - check node resources and taints"
fi

if [ $FAILED_EVENTS -gt 0 ]; then
    echo "   🚨 URGENT: $FAILED_EVENTS pods stuck pending >5min - check resource constraints, node availability, and taints"
fi

# Additional recommendation for high historical failure count
if [ $TOTAL_FAILED_EVENTS -gt 1000 ] && [ $FAILED_EVENTS -eq 0 ]; then
    echo "   ℹ️  High historical failed events ($TOTAL_FAILED_EVENTS) but no currently stuck pods - likely due to cluster scaling/retries"
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
