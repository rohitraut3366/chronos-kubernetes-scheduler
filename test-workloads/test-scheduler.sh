#!/bin/bash

# Chronos Scheduler Test Script
# This script helps test and monitor our intelligent bin-packing scheduler

set -e

echo "🎯 Chronos Kubernetes Scheduler Test Suite"
echo "=========================================="

# Check if scheduler is running
echo "📊 Checking scheduler status..."
kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler

scheduler_ready=$(kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "False")

if [ "$scheduler_ready" != "True" ]; then
    echo "❌ Scheduler is not ready. Please wait for it to become ready before running tests."
    exit 1
fi

echo "✅ Scheduler is ready!"
echo ""

# Function to run a test scenario
run_test_scenario() {
    local scenario=$1
    local description=$2
    
    echo "🧪 Test Scenario: $scenario"
    echo "Description: $description"
    echo "----------------------------------------"
    
    case $scenario in
        "basic")
            echo "Applying basic test jobs..."
            kubectl apply -f test-workloads/chronos-test-suite.yaml
            ;;
        "stress")
            echo "Creating multiple jobs to test bin-packing..."
            for i in {1..5}; do
                cat << EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: stress-job-$i
  labels:
    test: chronos-scheduler-stress
spec:
  template:
    metadata:
      annotations:
        scheduling.workload.io/expected-duration-seconds: "$((180 + i * 60))"
      labels:
        test: chronos-scheduler-stress
    spec:
      schedulerName: chronos-kubernetes-scheduler
      restartPolicy: Never
      containers:
      - name: workload
        image: busybox:1.36
        command: ["sh", "-c", "echo 'Stress Job $i on node:' && hostname && sleep $((180 + i * 60))"]
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
EOF
            done
            ;;
        "mixed")
            echo "Creating mixed workload with different durations..."
            # Short jobs that should consolidate
            for i in {1..3}; do
                cat << EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: mixed-short-$i
  labels:
    test: chronos-scheduler-mixed
spec:
  template:
    metadata:
      annotations:
        scheduling.workload.io/expected-duration-seconds: "300"
      labels:
        test: chronos-scheduler-mixed
    spec:
      schedulerName: chronos-kubernetes-scheduler
      restartPolicy: Never
      containers:
      - name: workload
        image: busybox:1.36
        command: ["sh", "-c", "echo 'Mixed Short Job $i:' && hostname && sleep 300"]
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
EOF
            done
            
            # One long job
            cat << EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: mixed-long-1
  labels:
    test: chronos-scheduler-mixed
spec:
  template:
    metadata:
      annotations:
        scheduling.workload.io/expected-duration-seconds: "1200"
      labels:
        test: chronos-scheduler-mixed
    spec:
      schedulerName: chronos-kubernetes-scheduler
      restartPolicy: Never
      containers:
      - name: workload
        image: busybox:1.36
        command: ["sh", "-c", "echo 'Mixed Long Job:' && hostname && sleep 1200"]
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
EOF
            ;;
    esac
    
    echo ""
}

# Function to monitor test results
monitor_tests() {
    echo "📈 Monitoring Test Results"
    echo "=========================="
    
    echo ""
    echo "🔍 Job Status:"
    kubectl get jobs -l test=chronos-scheduler --sort-by=.metadata.creationTimestamp
    
    echo ""
    echo "🖥️  Pod Distribution by Node:"
    kubectl get pods -l test=chronos-scheduler -o wide --sort-by=.spec.nodeName
    
    echo ""  
    echo "📋 Scheduler Events:"
    kubectl get events --sort-by=.firstTimestamp | grep chronos-kubernetes-scheduler || echo "No scheduler events found"
    
    echo ""
    echo "📊 Node Resource Usage:"
    kubectl top nodes 2>/dev/null || echo "Metrics not available (install metrics-server)"
}

# Function to analyze scheduling decisions  
analyze_scheduling() {
    echo "🔬 Analyzing Scheduling Decisions"
    echo "================================="
    
    echo ""
    echo "📍 Jobs scheduled on each node:"
    for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
        job_count=$(kubectl get pods -l test=chronos-scheduler --field-selector spec.nodeName=$node --no-headers 2>/dev/null | wc -l)
        if [ $job_count -gt 0 ]; then
            echo "  Node $node: $job_count jobs"
            kubectl get pods -l test=chronos-scheduler --field-selector spec.nodeName=$node -o jsonpath='{range .items[*]}{.metadata.annotations.scheduling\.workload\.io/expected-duration-seconds}{" "}{.metadata.name}{"\n"}{end}' | while read duration name; do
                if [ -n "$duration" ]; then
                    echo "    - $name: ${duration}s duration"
                else
                    echo "    - $name: no duration specified"
                fi
            done
        fi
    done
    
    echo ""
    echo "🎯 Expected Behavior:"
    echo "  - Jobs with similar or compatible durations should be on the same node (bin-packing)"
    echo "  - Short jobs should consolidate together"
    echo "  - Long jobs should minimize extensions on busy nodes"
    echo "  - Empty nodes should be avoided (Karpenter-friendly)"
}

# Function to cleanup test resources
cleanup_tests() {
    echo "🧹 Cleaning up test resources..."
    kubectl delete jobs -l test=chronos-scheduler --ignore-not-found
    kubectl delete jobs -l test=chronos-scheduler-stress --ignore-not-found
    kubectl delete jobs -l test=chronos-scheduler-mixed --ignore-not-found
    echo "✅ Cleanup completed"
}

# Main menu
show_menu() {
    echo ""
    echo "Available test scenarios:"
    echo "1) basic    - Run basic test suite with different job durations"  
    echo "2) stress   - Create multiple jobs to test bin-packing behavior"
    echo "3) mixed    - Mixed workload test (short + long jobs)"
    echo "4) monitor  - Monitor current test results" 
    echo "5) analyze  - Analyze scheduling decisions"
    echo "6) cleanup  - Clean up all test resources"
    echo "7) all      - Run all tests sequentially"
    echo "8) exit     - Exit"
}

# Handle command line arguments
if [ $# -gt 0 ]; then
    case $1 in
        "basic"|"stress"|"mixed")
            run_test_scenario $1 "Running $1 test scenario"
            ;;
        "monitor")
            monitor_tests
            ;;
        "analyze") 
            analyze_scheduling
            ;;
        "cleanup")
            cleanup_tests
            ;;
        "all")
            echo "🚀 Running all test scenarios..."
            run_test_scenario "basic" "Basic functionality test"
            sleep 10
            run_test_scenario "stress" "Stress test for bin-packing"
            sleep 10  
            run_test_scenario "mixed" "Mixed workload test"
            sleep 5
            monitor_tests
            sleep 5
            analyze_scheduling
            ;;
        *)
            echo "Unknown scenario: $1"
            show_menu
            exit 1
            ;;
    esac
    exit 0
fi

# Interactive mode
while true; do
    show_menu
    read -p "Select test scenario (1-8): " choice
    
    case $choice in
        1) run_test_scenario "basic" "Basic functionality test" ;;
        2) run_test_scenario "stress" "Stress test for bin-packing" ;;
        3) run_test_scenario "mixed" "Mixed workload test" ;;
        4) monitor_tests ;;
        5) analyze_scheduling ;;
        6) cleanup_tests ;;
        7) 
            echo "🚀 Running all test scenarios..."
            run_test_scenario "basic" "Basic functionality test"
            sleep 10
            run_test_scenario "stress" "Stress test for bin-packing" 
            sleep 10
            run_test_scenario "mixed" "Mixed workload test"
            sleep 5
            monitor_tests
            sleep 5
            analyze_scheduling
            ;;
        8) echo "👋 Goodbye!"; break ;;
        *) echo "Invalid choice. Please select 1-8." ;;
    esac
    
    echo ""
    read -p "Press Enter to continue..."
done
