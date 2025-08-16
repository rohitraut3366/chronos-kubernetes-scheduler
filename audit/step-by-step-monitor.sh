#!/bin/bash

# Step-by-Step Scheduler Monitoring Script
# This script provides detailed real-time monitoring of scheduler decisions

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m'

# Real-time scheduler log monitoring
monitor_scheduler_logs() {
    local pod_name="$1"
    local duration="${2:-30}"
    
    echo -e "${CYAN}🔍 Monitoring Scheduler Logs for pod: $pod_name (${duration}s)${NC}"
    echo "================================================================"
    
    timeout $duration kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler -f --tail=0 | while read line; do
        if echo "$line" | grep -q "$pod_name"; then
            if echo "$line" | grep -q "Scoring pod"; then
                echo -e "${BLUE}📊 SCORING:${NC} $line"
            elif echo "$line" | grep -q "Score.*optimized"; then
                echo -e "${GREEN}🎯 SCORE RESULT:${NC} $line"
            elif echo "$line" | grep -q "NormalizedScore"; then
                echo -e "${PURPLE}📈 NORMALIZED:${NC} $line"
            elif echo "$line" | grep -q "Successfully bound"; then
                echo -e "${GREEN}✅ FINAL DECISION:${NC} $line"
                break
            else
                echo -e "${YELLOW}📝 LOG:${NC} $line"
            fi
        fi
    done
}

# Show current state before scheduling
show_pre_scheduling_state() {
    echo -e "${BLUE}📊 PRE-SCHEDULING CLUSTER STATE${NC}"
    echo "==============================="
    
    echo -e "${CYAN}Available Nodes:${NC}"
    kubectl get nodes -o custom-columns="NAME:.metadata.name,STATUS:.status.conditions[?(@.type=='Ready')].status,CPU:.status.allocatable.cpu,MEMORY:.status.allocatable.memory" --no-headers
    
    echo ""
    echo -e "${CYAN}Current Job Distribution:${NC}"
    for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
        local job_count=$(kubectl get pods -l test=audit --field-selector spec.nodeName=$node --no-headers 2>/dev/null | wc -l)
        if [ $job_count -gt 0 ]; then
            echo "  $node: $job_count audit jobs"
            kubectl get pods -l test=audit --field-selector spec.nodeName=$node -o custom-columns="JOB:.metadata.labels.batch\.kubernetes\.io/job-name,DURATION:.metadata.annotations.scheduling\.workload\.io/expected-duration-seconds,STATUS:.status.phase" --no-headers 2>/dev/null | while read job duration status; do
                echo "    - $job: ${duration:-N/A}s ($status)"
            done
        else
            echo "  $node: empty"
        fi
    done
    echo ""
}

# Detailed analysis of scheduling decision
analyze_scheduling_decision() {
    local job_name="$1"
    local expected_duration="$2"
    
    echo -e "${PURPLE}🔬 DETAILED SCHEDULING ANALYSIS FOR: $job_name${NC}"
    echo "=================================================="
    
    # Wait for pod to be created
    echo -e "${YELLOW}⏳ Waiting for pod to be created...${NC}"
    local pod_name=""
    for i in {1..30}; do
        pod_name=$(kubectl get pods -l batch.kubernetes.io/job-name=$job_name -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
        if [ -n "$pod_name" ]; then
            break
        fi
        sleep 1
    done
    
    if [ -z "$pod_name" ]; then
        echo -e "${RED}❌ Pod not found for job $job_name${NC}"
        return 1
    fi
    
    echo -e "${GREEN}✅ Pod found: $pod_name${NC}"
    
    # Monitor in background and capture output
    echo -e "${CYAN}🔍 Capturing scheduling decision...${NC}"
    monitor_scheduler_logs "$pod_name" 20 &
    local monitor_pid=$!
    
    # Wait for scheduling to complete
    echo -e "${YELLOW}⏳ Waiting for scheduling to complete...${NC}"
    kubectl wait --for=condition=PodScheduled pod/$pod_name --timeout=30s >/dev/null 2>&1
    
    # Stop monitoring
    kill $monitor_pid 2>/dev/null || true
    wait $monitor_pid 2>/dev/null || true
    
    # Get final assignment
    local assigned_node=$(kubectl get pod $pod_name -o jsonpath='{.spec.nodeName}' 2>/dev/null)
    if [ -n "$assigned_node" ]; then
        echo -e "${GREEN}🎯 FINAL ASSIGNMENT: $pod_name → $assigned_node${NC}"
        
        # Show node state after assignment
        echo -e "${CYAN}📊 Node state after assignment:${NC}"
        local jobs_on_node=$(kubectl get pods -l test=audit --field-selector spec.nodeName=$assigned_node --no-headers 2>/dev/null | wc -l)
        echo "  Jobs on $assigned_node: $jobs_on_node"
        
        kubectl get pods -l test=audit --field-selector spec.nodeName=$assigned_node -o custom-columns="JOB:.metadata.labels.batch\.kubernetes\.io/job-name,DURATION:.metadata.annotations.scheduling\.workload\.io/expected-duration-seconds,STATUS:.status.phase" --no-headers 2>/dev/null | while read job duration status; do
            echo "    - $job: ${duration:-N/A}s ($status)"
        done
        
        # Explain the decision
        echo -e "${BLUE}💡 DECISION RATIONALE:${NC}"
        if [ $jobs_on_node -gt 1 ]; then
            echo "  ✅ CONSOLIDATION: Job was placed on a node with existing work"
            if [ -n "$expected_duration" ] && [ "$expected_duration" -lt 600 ]; then
                echo "  ✅ BIN-PACKING: Short job ($expected_duration s) likely fits within existing work windows"
            fi
        else
            echo "  ℹ️  ISOLATED: Job was placed on a node with no other audit jobs"
            echo "     This could be due to: resource constraints, node preferences, or algorithm optimization"
        fi
    else
        echo -e "${RED}❌ SCHEDULING FAILED: Pod not assigned to any node${NC}"
        kubectl describe pod $pod_name | tail -10
    fi
    
    echo ""
}

# Main step-by-step monitoring function
step_by_step_test() {
    local scenario="$1"
    local job_name="$2"  
    local duration="$3"
    local description="$4"
    
    echo -e "${PURPLE}🧪 STEP-BY-STEP TEST: $scenario${NC}"
    echo "========================================"
    echo -e "Job: $job_name"
    echo -e "Duration: $duration seconds"
    echo -e "Description: $description"
    echo ""
    
    # Show state before
    show_pre_scheduling_state
    
    # Deploy the job
    echo -e "${YELLOW}🚀 Deploying job: $job_name${NC}"
    cat << EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: $job_name
  labels:
    test: audit
    scenario: step-by-step
spec:
  template:
    metadata:
      annotations:
        scheduling.workload.io/expected-duration-seconds: "$duration"
      labels:
        test: audit
        scenario: step-by-step
    spec:
      schedulerName: chronos-kubernetes-scheduler
      restartPolicy: Never
      containers:
      - name: workload
        image: busybox:1.36
        command: ["sh", "-c", "echo 'Job $job_name started on' \$(hostname) 'at' \$(date) 'for ${duration}s' && sleep $duration"]
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
EOF
    
    # Analyze the scheduling decision
    analyze_scheduling_decision "$job_name" "$duration"
    
    echo -e "${GREEN}✅ Step completed. Press Enter to continue to next step...${NC}"
    read
}

# Interactive step-by-step mode
interactive_mode() {
    echo -e "${PURPLE}🚀 INTERACTIVE STEP-BY-STEP SCHEDULER AUDIT${NC}"
    echo "============================================"
    echo ""
    echo "This will walk through each scheduling scenario step by step,"
    echo "showing detailed logs and analysis for each decision."
    echo ""
    
    # Clean up first
    echo -e "${YELLOW}🧹 Cleaning up any existing audit jobs...${NC}"
    kubectl delete jobs -l test=audit --ignore-not-found=true >/dev/null 2>&1
    sleep 3
    
    # Step 1: Baseline
    step_by_step_test "Baseline" "step-baseline" "600" "Single 10-minute job to establish baseline"
    
    # Step 2: Short consolidation
    step_by_step_test "Consolidation" "step-consolidate" "300" "5-minute job - should consolidate with baseline"
    
    # Step 3: Another short job
    step_by_step_test "Multi-Consolidation" "step-multi" "240" "4-minute job - should join the consolidation"
    
    # Step 4: Long extension job
    step_by_step_test "Extension" "step-extend" "900" "15-minute job - tests extension minimization"
    
    # Step 5: Very short job
    step_by_step_test "Optimal-Fit" "step-optimal" "180" "3-minute job - should find optimal fit"
    
    # Final summary
    echo -e "${BLUE}📊 FINAL CLUSTER STATE SUMMARY${NC}"
    echo "==============================="
    kubectl get jobs -l test=audit --sort-by=.metadata.name
    echo ""
    kubectl get pods -l test=audit -o wide --sort-by=.spec.nodeName
    echo ""
    
    # Node distribution analysis
    echo -e "${CYAN}📈 Final Node Distribution Analysis:${NC}"
    for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
        local job_count=$(kubectl get pods -l test=audit --field-selector spec.nodeName=$node --no-headers 2>/dev/null | wc -l)
        if [ $job_count -gt 0 ]; then
            echo -e "${GREEN}  $node: $job_count jobs${NC}"
            kubectl get pods -l test=audit --field-selector spec.nodeName=$node -o custom-columns="JOB:.metadata.labels.batch\.kubernetes\.io/job-name,DURATION:.metadata.annotations.scheduling\.workload\.io/expected-duration-seconds" --no-headers 2>/dev/null | while read job duration; do
                echo "    - $job: ${duration:-N/A}s"
            done
        else
            echo -e "${YELLOW}  $node: empty${NC}"
        fi
    done
    
    echo ""
    echo -e "${GREEN}🎉 Interactive audit completed!${NC}"
}

# Command line interface
if [ "$1" = "interactive" ]; then
    interactive_mode
else
    echo "Usage: $0 interactive"
    echo ""
    echo "This script provides detailed step-by-step monitoring of scheduler decisions."
    echo "Run with 'interactive' to start the guided audit process."
fi
