#!/bin/bash

# Chronos Scheduler Long-Term Test Suite
# Tests consolidation, bin-packing, and scheduling intelligence over 2-4+ hours
# Usage: ./long-term-scheduler-test.sh [duration_hours] [test_mode]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
DURATION_HOURS=${1:-4}  # Default 4 hours
TEST_MODE=${2:-comprehensive}  # comprehensive, consolidation, stress
NAMESPACE=${CHRONOS_NAMESPACE:-default}
SCHEDULER_NAME="chronos-kubernetes-scheduler"
LOG_FILE="chronos-longterm-$(date +%Y%m%d-%H%M%S).log"
RESULTS_DIR="results-$(date +%Y%m%d-%H%M%S)"

# Global counters
TOTAL_JOBS_CREATED=0
TOTAL_JOBS_COMPLETED=0
CONSOLIDATION_SUCCESSES=0
SCHEDULING_DECISIONS=0
TEST_START_TIME=$(date +%s)

# Job templates with different durations and resource requirements
declare -A JOB_TEMPLATES
JOB_TEMPLATES[short]="300,100m,128Mi"      # 5 minutes
JOB_TEMPLATES[medium]="900,150m,256Mi"     # 15 minutes  
JOB_TEMPLATES[long]="1800,200m,512Mi"      # 30 minutes
JOB_TEMPLATES[very_long]="3600,250m,1Gi"  # 1 hour
JOB_TEMPLATES[ultra_long]="7200,300m,2Gi" # 2 hours

# Logging function
log() {
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo -e "[$timestamp] $1" | tee -a "$LOG_FILE"
}

# Test result tracking
test_result() {
    local test_name="$1"
    local result="$2" 
    local details="$3"
    
    if [ "$result" = "PASS" ]; then
        log "${GREEN}✅ $test_name: PASSED${NC} - $details"
    elif [ "$result" = "FAIL" ]; then
        log "${RED}❌ $test_name: FAILED${NC} - $details"
    else
        log "${YELLOW}⚠️  $test_name: $result${NC} - $details"
    fi
}

# Initialize test environment
setup_test_environment() {
    log "${PURPLE}🚀 CHRONOS LONG-TERM SCHEDULER TEST SUITE${NC}"
    log "========================================"
    log "Duration: $DURATION_HOURS hours"
    log "Mode: $TEST_MODE"
    log "Namespace: $NAMESPACE"
    log "Results Directory: $RESULTS_DIR"
    log ""
    
    # Create results directory
    mkdir -p "$RESULTS_DIR"
    
    # Check scheduler readiness
    log "${BLUE}🔍 Checking Chronos Scheduler Status${NC}"
    if kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler --no-headers 2>/dev/null | grep -q Running; then
        log "${GREEN}✅ Chronos scheduler is running${NC}"
    else
        log "${RED}❌ Chronos scheduler not found or not running${NC}"
        exit 1
    fi
    
    # Clean up any existing test jobs
    log "${YELLOW}🧹 Cleaning up existing test jobs${NC}"
    kubectl delete jobs -l test=chronos-longterm --ignore-not-found=true >/dev/null 2>&1
    
    # Create monitoring script
    create_monitoring_script
}

# Create background monitoring script
create_monitoring_script() {
    cat << 'MONITOR_EOF' > "$RESULTS_DIR/monitor.sh"
#!/bin/bash
while true; do
    timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] === CLUSTER STATE ===" >> monitor.log
    kubectl get jobs -l test=chronos-longterm -o wide >> monitor.log 2>&1
    kubectl get pods -l test=chronos-longterm -o wide >> monitor.log 2>&1
    echo "" >> monitor.log
    
    # Get scheduler logs
    kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler --tail=50 | grep -E "(Chronos|Score|optimized)" | tail -10 >> scheduler-decisions.log 2>/dev/null || true
    
    sleep 30
done
MONITOR_EOF
    chmod +x "$RESULTS_DIR/monitor.sh"
}

# Create a job with specific parameters
create_job() {
    local job_name="$1"
    local duration="$2"
    local cpu="$3"
    local memory="$4"
    local job_type="$5"
    local sequence="$6"
    
    log "${CYAN}📦 Creating job: $job_name (${duration}s, $cpu CPU, $memory memory)${NC}"
    
    cat << EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: $job_name
  labels:
    test: chronos-longterm
    job-type: $job_type
    sequence: "$sequence"
spec:
  backoffLimit: 1
  activeDeadlineSeconds: $(( duration + 300 ))  # 5 min buffer
  template:
    metadata:
      annotations:
        scheduling.workload.io/expected-duration-seconds: "$duration"
      labels:
        test: chronos-longterm
        job-type: $job_type
        sequence: "$sequence"
    spec:
      schedulerName: $SCHEDULER_NAME
      restartPolicy: Never
      containers:
      - name: workload
        image: ubuntu:22.04
        command: ["/bin/bash", "-c"]
        args:
        - |
          echo "🚀 Job $job_name started on \$(hostname) at \$(date)"
          echo "Type: $job_type, Duration: ${duration}s, Sequence: $sequence"
          echo "CPU: $cpu, Memory: $memory"
          echo "Expected completion: \$(date -d "+${duration} seconds")"
          
          start_time=\$(date +%s)
          target_end=\$((start_time + duration))
          
          # Create heartbeat file
          echo "Job $job_name heartbeat log" > /tmp/heartbeat.log
          
          # Main execution loop with detailed logging
          while [ \$(date +%s) -lt \$target_end ]; do
            current_time=\$(date +%s)
            elapsed=\$((current_time - start_time))
            remaining=\$((target_end - current_time))
            
            # Progress updates every minute
            if [ \$((elapsed % 60)) -eq 0 ]; then
              minutes=\$((elapsed / 60))
              echo "⏳ \${minutes}min elapsed: Job $job_name still running on \$(hostname)"
            fi
            
            # Detailed progress every 10 minutes
            if [ \$((elapsed % 600)) -eq 0 ] && [ \$elapsed -gt 0 ]; then
              echo "📊 Progress: Job $job_name - \${minutes} minutes / \$(((target_end - start_time) / 60)) minutes total"
              echo "   - Node: \$(hostname)"
              echo "   - Memory usage: \$(cat /proc/meminfo | grep MemAvailable)"
              echo "   - Remaining: \$((remaining / 60)) minutes"
            fi
            
            # Update heartbeat
            echo "\$(date): \$elapsed seconds elapsed" >> /tmp/heartbeat.log
            
            sleep 1
          done
          
          final_time=\$(date +%s)
          actual_duration=\$((final_time - start_time))
          echo "🎉 SUCCESS: Job $job_name completed after \$actual_duration seconds"
          echo "Expected: ${duration}s, Actual: \${actual_duration}s"
          echo "Node: \$(hostname), Completion: \$(date)"
        resources:
          requests:
            cpu: $cpu
            memory: $memory
          limits:
            cpu: $(echo $cpu | sed 's/m$//')m
            memory: $memory
EOF
    
    TOTAL_JOBS_CREATED=$((TOTAL_JOBS_CREATED + 1))
    
    # Wait for pod to be scheduled and log the decision
    sleep 5
    local pod_name=$(kubectl get pods -l batch.kubernetes.io/job-name=$job_name -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    if [ -n "$pod_name" ]; then
        local assigned_node=$(kubectl get pod $pod_name -o jsonpath='{.spec.nodeName}' 2>/dev/null)
        log "${GREEN}✅ Job $job_name scheduled to node: $assigned_node${NC}"
        
        # Capture scheduler decision
        kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler --tail=100 | \
            grep "$pod_name" >> "$RESULTS_DIR/scheduler-decisions-$job_name.log" 2>/dev/null || true
    else
        log "${RED}❌ Job $job_name failed to schedule${NC}"
    fi
}

# Phase 1: Baseline establishment (30 minutes)
phase_1_baseline() {
    log "${PURPLE}📋 PHASE 1: BASELINE ESTABLISHMENT (30 minutes)${NC}"
    log "=============================================="
    
    # Create initial long-running jobs to establish baselines
    create_job "baseline-long-1" "1800" "200m" "512Mi" "baseline" "1"
    sleep 60
    create_job "baseline-long-2" "1800" "200m" "512Mi" "baseline" "2"
    sleep 60
    create_job "baseline-medium-1" "900" "150m" "256Mi" "baseline" "3"
    
    log "${CYAN}⏳ Waiting 30 minutes for baseline establishment...${NC}"
    sleep 1800  # 30 minutes
}

# Phase 2: Consolidation testing (60 minutes)
phase_2_consolidation() {
    log "${PURPLE}📋 PHASE 2: CONSOLIDATION TESTING (60 minutes)${NC}"
    log "=============================================="
    
    local phase_start=$(date +%s)
    local phase_end=$((phase_start + 3600))  # 1 hour
    
    local job_counter=1
    
    while [ $(date +%s) -lt $phase_end ]; do
        # Test different consolidation scenarios
        case $((job_counter % 4)) in
            0)
                # Short jobs that should bin-pack
                create_job "consolidation-short-$job_counter" "300" "100m" "128Mi" "consolidation" "$job_counter"
                ;;
            1)
                # Medium jobs that might extend
                create_job "consolidation-medium-$job_counter" "600" "150m" "256Mi" "consolidation" "$job_counter"
                ;;
            2)
                # Jobs that should fit within existing windows
                create_job "consolidation-fit-$job_counter" "450" "120m" "192Mi" "consolidation" "$job_counter"
                ;;
            3)
                # Longer jobs testing extension logic
                create_job "consolidation-extend-$job_counter" "1200" "180m" "384Mi" "consolidation" "$job_counter"
                ;;
        esac
        
        # Analyze consolidation after each job
        analyze_current_consolidation "$job_counter"
        
        job_counter=$((job_counter + 1))
        
        # Wait between job creations (varies to test different scenarios)
        sleep $((120 + (job_counter % 3) * 60))  # 2-5 minutes between jobs
    done
}

# Phase 3: Stress testing (90 minutes)  
phase_3_stress() {
    log "${PURPLE}📋 PHASE 3: STRESS TESTING (90 minutes)${NC}"
    log "========================================"
    
    local phase_start=$(date +%s)
    local phase_end=$((phase_start + 5400))  # 90 minutes
    
    local job_counter=1
    
    # Create multiple jobs rapidly to test scheduler performance
    while [ $(date +%s) -lt $phase_end ]; do
        # Burst creation: 3 jobs quickly, then wait
        for burst in {1..3}; do
            local template_key=$(get_random_template)
            IFS=',' read -r duration cpu memory <<< "${JOB_TEMPLATES[$template_key]}"
            
            create_job "stress-${template_key}-${job_counter}-${burst}" "$duration" "$cpu" "$memory" "stress" "$job_counter"
            sleep 30  # Short delay between burst jobs
        done
        
        # Longer wait between bursts
        sleep $((300 + (job_counter % 5) * 60))  # 5-9 minutes
        
        job_counter=$((job_counter + 1))
        
        # Cleanup completed jobs periodically to prevent resource exhaustion
        if [ $((job_counter % 10)) -eq 0 ]; then
            cleanup_completed_jobs
        fi
    done
}

# Phase 4: Long-term stability (remaining time)
phase_4_stability() {
    log "${PURPLE}📋 PHASE 4: LONG-TERM STABILITY${NC}"
    log "==============================="
    
    local remaining_time=$((DURATION_HOURS * 3600 - ($(date +%s) - TEST_START_TIME)))
    
    if [ $remaining_time -gt 0 ]; then
        log "${CYAN}⏳ Running stability phase for $((remaining_time / 60)) minutes...${NC}"
        
        local job_counter=1
        local phase_end=$(($(date +%s) + remaining_time))
        
        while [ $(date +%s) -lt $phase_end ]; do
            # Create mix of very long jobs
            create_job "stability-ultra-$job_counter" "7200" "300m" "2Gi" "stability" "$job_counter"
            sleep 900  # 15 minutes between very long jobs
            
            job_counter=$((job_counter + 1))
        done
    fi
}

# Analyze current consolidation state
analyze_current_consolidation() {
    local job_sequence="$1"
    
    log "${BLUE}📊 CONSOLIDATION ANALYSIS (Job $job_sequence)${NC}"
    
    # Get current job distribution across nodes
    local node_distribution=$(kubectl get pods -l test=chronos-longterm --no-headers 2>/dev/null | \
        awk '{print $7}' | sort | uniq -c | sort -nr)
    
    if [ -n "$node_distribution" ]; then
        echo "$node_distribution" > "$RESULTS_DIR/node-distribution-job-$job_sequence.txt"
        
        local total_nodes=$(echo "$node_distribution" | wc -l)
        local total_pods=$(kubectl get pods -l test=chronos-longterm --no-headers 2>/dev/null | wc -l)
        local consolidation_ratio=$(echo "scale=2; $total_pods / $total_nodes" | bc 2>/dev/null || echo "N/A")
        
        log "  Current consolidation ratio: $consolidation_ratio pods/node ($total_pods pods on $total_nodes nodes)"
        
        # Check for successful consolidation (ratio > 2.0 indicates good consolidation)
        if [ "$consolidation_ratio" != "N/A" ] && [ "$(echo "$consolidation_ratio > 2.0" | bc 2>/dev/null)" = "1" ]; then
            CONSOLIDATION_SUCCESSES=$((CONSOLIDATION_SUCCESSES + 1))
            log "${GREEN}✅ Good consolidation detected${NC}"
        fi
    fi
}

# Get random job template
get_random_template() {
    local templates=(short medium long very_long)
    local random_index=$((RANDOM % ${#templates[@]}))
    echo "${templates[$random_index]}"
}

# Cleanup completed jobs
cleanup_completed_jobs() {
    log "${YELLOW}🧹 Cleaning up completed jobs${NC}"
    
    # Get completed jobs
    local completed_jobs=$(kubectl get jobs -l test=chronos-longterm --no-headers 2>/dev/null | \
        awk '$2=="Complete" {print $1}')
    
    if [ -n "$completed_jobs" ]; then
        echo "$completed_jobs" | while read job; do
            kubectl delete job "$job" --ignore-not-found=true >/dev/null 2>&1
            TOTAL_JOBS_COMPLETED=$((TOTAL_JOBS_COMPLETED + 1))
            log "  Cleaned up completed job: $job"
        done
    fi
}

# Generate comprehensive final report
generate_final_report() {
    local test_end_time=$(date +%s)
    local total_duration=$((test_end_time - TEST_START_TIME))
    
    log "${PURPLE}📋 FINAL COMPREHENSIVE REPORT${NC}"
    log "==============================="
    
    # Test duration and completion
    log "Total Test Duration: $((total_duration / 3600))h $((total_duration % 3600 / 60))m"
    log "Jobs Created: $TOTAL_JOBS_CREATED"
    log "Jobs Completed: $TOTAL_JOBS_COMPLETED"
    log "Consolidation Successes: $CONSOLIDATION_SUCCESSES"
    
    # Current cluster state
    log "${CYAN}📊 Final Cluster State:${NC}"
    kubectl get jobs -l test=chronos-longterm -o wide > "$RESULTS_DIR/final-jobs-state.txt" 2>&1
    kubectl get pods -l test=chronos-longterm -o wide > "$RESULTS_DIR/final-pods-state.txt" 2>&1
    
    # Node distribution analysis
    log "${CYAN}📈 Final Node Distribution:${NC}"
    local final_distribution=$(kubectl get pods -l test=chronos-longterm --no-headers 2>/dev/null | \
        awk '{print $7}' | sort | uniq -c | sort -nr)
    
    if [ -n "$final_distribution" ]; then
        echo "$final_distribution" | tee "$RESULTS_DIR/final-node-distribution.txt"
        
        local final_nodes=$(echo "$final_distribution" | wc -l)
        local final_pods=$(kubectl get pods -l test=chronos-longterm --no-headers 2>/dev/null | wc -l)
        local final_ratio=$(echo "scale=2; $final_pods / $final_nodes" | bc 2>/dev/null || echo "N/A")
        
        log "Final Consolidation Ratio: $final_ratio ($final_pods pods on $final_nodes nodes)"
    fi
    
    # Scheduler effectiveness metrics
    log "${CYAN}🎯 Scheduler Effectiveness:${NC}"
    local completion_rate=$(echo "scale=1; $TOTAL_JOBS_COMPLETED * 100 / $TOTAL_JOBS_CREATED" | bc 2>/dev/null || echo "N/A")
    local consolidation_rate=$(echo "scale=1; $CONSOLIDATION_SUCCESSES * 100 / $TOTAL_JOBS_CREATED" | bc 2>/dev/null || echo "N/A")
    
    log "Job Completion Rate: ${completion_rate}%"
    log "Consolidation Success Rate: ${consolidation_rate}%"
    
    # Generate summary file
    cat << REPORT_EOF > "$RESULTS_DIR/test-summary.txt"
Chronos Scheduler Long-Term Test Summary
========================================
Date: $(date)
Duration: $((total_duration / 3600))h $((total_duration % 3600 / 60))m
Mode: $TEST_MODE

Results:
- Jobs Created: $TOTAL_JOBS_CREATED
- Jobs Completed: $TOTAL_JOBS_COMPLETED
- Completion Rate: ${completion_rate}%
- Consolidation Successes: $CONSOLIDATION_SUCCESSES
- Consolidation Rate: ${consolidation_rate}%
- Final Consolidation Ratio: $final_ratio

Files Generated:
- Full logs: $LOG_FILE
- Node distributions: node-distribution-*.txt
- Scheduler decisions: scheduler-decisions-*.log
- Final states: final-*.txt
REPORT_EOF
    
    log "${GREEN}🎉 LONG-TERM TEST COMPLETED SUCCESSFULLY!${NC}"
    log "Results saved in: $RESULTS_DIR/"
    log "Summary: $RESULTS_DIR/test-summary.txt"
}

# Main test execution
main() {
    setup_test_environment
    
    # Start background monitoring
    log "${YELLOW}🔄 Starting background monitoring${NC}"
    cd "$RESULTS_DIR" && ./monitor.sh &
    local monitor_pid=$!
    cd ..
    
    # Execute test phases based on mode
    case $TEST_MODE in
        "consolidation")
            phase_1_baseline
            phase_2_consolidation
            ;;
        "stress")
            phase_1_baseline
            phase_3_stress
            ;;
        "comprehensive"|*)
            phase_1_baseline
            phase_2_consolidation
            phase_3_stress
            phase_4_stability
            ;;
    esac
    
    # Stop monitoring
    kill $monitor_pid 2>/dev/null || true
    
    # Generate final report
    generate_final_report
    
    # Optional cleanup
    if [ "${CLEANUP_AFTER_TEST:-true}" = "true" ]; then
        log "${YELLOW}🧹 Final cleanup${NC}"
        kubectl delete jobs -l test=chronos-longterm --ignore-not-found=true >/dev/null 2>&1
    fi
}

# Signal handling
trap 'log "Test interrupted. Generating partial report..."; generate_final_report; exit 1' INT TERM

# Check dependencies
if ! command -v kubectl &> /dev/null; then
    echo "kubectl is required but not installed."
    exit 1
fi

if ! command -v bc &> /dev/null; then
    echo "bc is required but not installed. Install with: brew install bc"
    exit 1
fi

# Run main function
main "$@"
