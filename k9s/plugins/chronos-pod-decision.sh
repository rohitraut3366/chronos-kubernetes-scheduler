#!/bin/bash
# K9s Plugin: Show Chronos scheduling decision for specific pod
# Usage: Called from K9s when Ctrl-D is pressed on a pod

set -x

# Colors for terminal output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m' # No Color
BOLD='\033[1m'

# Function to find Chronos scheduler leader pod
find_scheduler_pod() {
    echo -e "${BLUE}🔍 Finding Chronos scheduler leader...${NC}"
    
    # Get all scheduler pods
    local scheduler_pods
    echo -e "${CYAN}Running: kubectl get pods -n codeship-custom-scheduler-eks -l app.kubernetes.io/name=chronos-kubernetes-scheduler${NC}"
    
    scheduler_pods=$(kubectl get pods -n codeship-custom-scheduler-eks -l app.kubernetes.io/name=chronos-kubernetes-scheduler -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>&1)
    local kubectl_exit_code=$?
    
    if [[ $kubectl_exit_code -ne 0 ]]; then
        echo -e "${RED}❌ kubectl command failed with exit code: $kubectl_exit_code${NC}"
        echo -e "${YELLOW}Error output:${NC}"
        echo "$scheduler_pods"
        return 1
    fi
    
    if [[ -z "$scheduler_pods" ]]; then
        echo -e "${YELLOW}⚠️  No Chronos scheduler pods found${NC}"
        return 1
    fi
    
    local pod_count=$(echo "$scheduler_pods" | wc -l | tr -d ' ')
    echo -e "${CYAN}📊 Found $pod_count scheduler pod(s)${NC}"
    
    # If only one pod, that's our scheduler
    if [[ "$pod_count" -eq 1 ]]; then
        echo -e "${GREEN}✅ Single scheduler pod found${NC}"
        echo "$scheduler_pods"
        return 0
    fi
    
    # Multiple pods - find the leader by checking total scheduling activity
    echo -e "${YELLOW}🔍 Multiple pods found, identifying leader...${NC}"
    local leader_pod=""
    local max_total_logs=0
    
    while IFS= read -r pod; do
        if [[ -n "$pod" ]]; then
            local namespace=$(echo "$pod" | cut -d'/' -f1)
            local pod_name=$(echo "$pod" | cut -d'/' -f2)
            
            # Check all available logs for scheduling activity
            local total_logs
            total_logs=$(kubectl logs -n "$namespace" "$pod_name" 2>/dev/null | grep -E "(Successfully bound|Attempting to schedule)" | wc -l | tr -d ' ')
            
            echo -e "${CYAN}  $pod: $total_logs total scheduling events${NC}"
            
            if [[ "$total_logs" -gt "$max_total_logs" ]]; then
                max_total_logs="$total_logs"
                leader_pod="$pod"
            fi
        fi
    done <<< "$scheduler_pods"
    
    if [[ -n "$leader_pod" ]]; then
        echo -e "${GREEN}✅ Leader identified: $leader_pod${NC}"
        echo "$leader_pod"
        return 0
    else
        # Fallback: try to find leader via lease
        echo -e "${YELLOW}🔍 Checking leader election lease...${NC}"
        local lease_holder
        lease_holder=$(kubectl get lease -n codeship-custom-scheduler-eks -o jsonpath='{range .items[*]}{.spec.holderIdentity}{"\n"}{end}' | head -1 2>/dev/null || true)
        
        if [[ -n "$lease_holder" ]]; then
            # Match lease holder to pod
            while IFS= read -r pod; do
                if [[ -n "$pod" && "$pod" == *"$lease_holder"* ]]; then
                    echo -e "${GREEN}✅ Leader found via lease: $pod${NC}"
                    echo "$pod"
                    return 0
                fi
            done <<< "$scheduler_pods"
        fi
        
        # Final fallback: just use the first pod
        echo -e "${YELLOW}⚠️  Could not determine leader, using first pod${NC}"
        echo "$scheduler_pods" | head -1
        return 0
    fi
}

# Pod name is now passed directly as argument from k9s plugin

# Function to analyze specific pod scheduling decision
analyze_pod_decision() {
    local pod_name="$1"
    local scheduler_pod="$2"
    local namespace=$(echo "$scheduler_pod" | cut -d'/' -f1)
    local pod_name_only=$(echo "$scheduler_pod" | cut -d'/' -f2)
    
    echo -e "${BOLD}${CYAN}Chronos Scheduling Decision for Pod: ${GREEN}$pod_name${NC}\n"
    
    # Get scheduler logs and filter for this specific pod
    local logs
    logs=$(kubectl logs -n "$namespace" "$pod_name_only" 2>/dev/null | grep -E "$pod_name" || true)
    
    if [[ -z "$logs" ]]; then
        echo -e "${RED}❌ No scheduling logs found for pod: $pod_name${NC}"
        echo "This could mean:"
        echo "• Pod was not scheduled by Chronos"
        echo "• Pod name doesn't match logs exactly"
        echo "• Logs have rotated out"
        return 1
    fi
    
    echo -e "${BLUE}📊 Found scheduling data for pod${NC}\n"
    
    # Parse the scheduling session
    local pod_duration=""
    local chosen_node=""
    local chosen_strategy=""
    local timestamp=""
    
    # Extract basic info
    pod_duration=$(echo "$logs" | grep -o "NewJob=[0-9]*s" | head -1 | cut -d'=' -f2 || echo "N/A")
    chosen_node=$(echo "$logs" | grep "Successfully bound pod" | grep "$pod_name" | sed -n 's/.*node="\([^"]*\)".*/\1/p' || echo "N/A")
    timestamp=$(echo "$logs" | head -1 | grep -o '^I[0-9]* [0-9:]*\.[0-9]*' | tr -d 'I' || echo "N/A")
    
    # Show key scheduling details
    echo -e "${BOLD}${CYAN}📋 SCHEDULING DECISION SUMMARY${NC}"
    echo "════════════════════════════════════════════════════════════════════════════════"
    printf "${BOLD}%-20s${NC} %s\n" "Pod Name:" "${GREEN}$pod_name${NC}"
    printf "${BOLD}%-20s${NC} %s\n" "Pod Duration:" "${YELLOW}$pod_duration${NC}"
    printf "${BOLD}%-20s${NC} %s\n" "Chosen Node:" "${GREEN}$chosen_node${NC}"
    printf "${BOLD}%-20s${NC} %s\n" "Scheduled At:" "${BLUE}$timestamp${NC}"
    
    # Count nodes evaluated from plugin output
    local nodes_evaluated=$(echo "$logs" | grep "plugin.go.*Node:" | wc -l | tr -d ' ')
    printf "${BOLD}%-20s${NC} %s\n" "Nodes Evaluated:" "${CYAN}$nodes_evaluated${NC}"
    
    echo "════════════════════════════════════════════════════════════════════════════════"
    
    echo -e "\n${BOLD}🎯 NODE EVALUATION DETAILS:${NC}"
    echo "┌─────────────────────────────────────┬─────────────┬─────────────────┬────────────┬────────────┐"
    echo "│ Node Name                           │ Strategy    │ Completion Time │ Raw Score  │ Norm Score │"
    echo "├─────────────────────────────────────┼─────────────┼─────────────────┼────────────┼────────────┤"
    
    # Parse node evaluations
    echo "$logs" | while IFS= read -r line; do
        # Parse strategy lines
        if [[ "$line" =~ BIN-PACKING.*Final=(-?[0-9]+) ]]; then
            node_name=$(echo "$line" | sed -n 's/.*Node \([^:]*\):.*/\1/p')
            completion_time=$(echo "$line" | sed -n 's/.*Existing=\([0-9]*s\).*/\1/p')
            raw_score="${BASH_REMATCH[1]}"
            strategy="BIN-PACKING"
            
            # Look for normalized score in following lines
            norm_score=$(echo "$logs" | grep "Node: $node_name" | grep "NormalizedScore:" | sed -n 's/.*NormalizedScore: \([0-9]*\).*/\1/p' | head -1)
            norm_score=${norm_score:-"N/A"}
            
            # Highlight chosen node
            if [[ "$node_name" == "$chosen_node" ]]; then
                printf "│ ${GREEN}%-35s${NC} │ ${CYAN}%-11s${NC} │ ${YELLOW}%-15s${NC} │ ${MAGENTA}%-10s${NC} │ ${BOLD}%-10s${NC} │\n" "$node_name" "$strategy" "$completion_time" "$raw_score" "$norm_score"
            else
                printf "│ %-35s │ %-11s │ %-15s │ %-10s │ %-10s │\n" "$node_name" "$strategy" "$completion_time" "$raw_score" "$norm_score"
            fi
            
        elif [[ "$line" =~ EXTENSION.*Final=(-?[0-9]+) ]]; then
            node_name=$(echo "$line" | sed -n 's/.*Node \([^:]*\):.*/\1/p')
            completion_time=$(echo "$line" | sed -n 's/.*Existing=\([0-9]*s\).*/\1/p')
            raw_score="${BASH_REMATCH[1]}"
            strategy="EXTENSION"
            
            norm_score=$(echo "$logs" | grep "Node: $node_name" | grep "NormalizedScore:" | sed -n 's/.*NormalizedScore: \([0-9]*\).*/\1/p' | head -1)
            norm_score=${norm_score:-"N/A"}
            
            if [[ "$node_name" == "$chosen_node" ]]; then
                printf "│ ${GREEN}%-35s${NC} │ ${YELLOW}%-11s${NC} │ ${YELLOW}%-15s${NC} │ ${MAGENTA}%-10s${NC} │ ${BOLD}%-10s${NC} │\n" "$node_name" "$strategy" "$completion_time" "$raw_score" "$norm_score"
            else
                printf "│ %-35s │ %-11s │ %-15s │ %-10s │ %-10s │\n" "$node_name" "$strategy" "$completion_time" "$raw_score" "$norm_score"
            fi
            
        elif [[ "$line" =~ EMPTY.*Final=(-?[0-9]+) ]]; then
            node_name=$(echo "$line" | sed -n 's/.*Node \([^:]*\):.*/\1/p')
            completion_time="0s"
            raw_score="${BASH_REMATCH[1]}"
            strategy="EMPTY NODE"
            
            norm_score=$(echo "$logs" | grep "Node: $node_name" | grep "NormalizedScore:" | sed -n 's/.*NormalizedScore: \([0-9]*\).*/\1/p' | head -1)
            norm_score=${norm_score:-"N/A"}
            
            if [[ "$node_name" == "$chosen_node" ]]; then
                printf "│ ${GREEN}%-35s${NC} │ ${MAGENTA}%-11s${NC} │ ${YELLOW}%-15s${NC} │ ${MAGENTA}%-10s${NC} │ ${BOLD}%-10s${NC} │\n" "$node_name" "$strategy" "$completion_time" "$raw_score" "$norm_score"
            else
                printf "│ %-35s │ %-11s │ %-15s │ %-10s │ %-10s │\n" "$node_name" "$strategy" "$completion_time" "$raw_score" "$norm_score"
            fi
        fi
    done
    
    echo "└─────────────────────────────────────┴─────────────┴─────────────────┴────────────┴────────────┘"
    
    echo -e "\n${BOLD}${GREEN}✅ Chosen Node: $chosen_node${NC}"
    
    # Determine strategy used
    chosen_strategy=$(echo "$logs" | grep "$chosen_node" | grep -E "(BIN-PACKING|EXTENSION|EMPTY)" | head -1 | sed -n 's/.* \(BIN-PACKING\|EXTENSION\|EMPTY NODE\).*/\1/p')
    if [[ -n "$chosen_strategy" ]]; then
        echo -e "${BOLD}🎯 Strategy Used: ${CYAN}$chosen_strategy${NC}"
    fi
    
    echo -e "\n${BOLD}${CYAN}📝 Legend:${NC}"
    echo -e "${GREEN}● Green node name${NC} = Chosen node"
    echo -e "${CYAN}● BIN-PACKING${NC} = Job fits in existing completion time"
    echo -e "${YELLOW}● EXTENSION${NC} = Job extends node completion time"
    echo -e "${MAGENTA}● EMPTY NODE${NC} = Node has no running jobs"
}

# Main execution
main() {
    clear
    
    # Find scheduler pod
    local scheduler_pod_output
    scheduler_pod_output=$(find_scheduler_pod)
    
    # Extract just the last line which contains the actual pod name
    local scheduler_pod=$(echo "$scheduler_pod_output" | tail -1)
    
    if [[ -z "$scheduler_pod" || "$scheduler_pod" == *"No Chronos scheduler pods found"* ]]; then
        echo -e "${RED}❌ Could not find Chronos scheduler pod${NC}"
        echo "Make sure the scheduler is running with label: app.kubernetes.io/name=chronos-kubernetes-scheduler in namespace: codeship-custom-scheduler-eks"
        echo -e "\n${YELLOW}Press any key to return to K9s...${NC}"
        read -n 1 -s
        exit 1
    fi
    
    # Show the scheduler identification results (excluding the final line)
    echo "$scheduler_pod_output" | sed '$d'
    
    # Get pod name from argument (passed by k9s)
    local pod_name="$1"
    
    if [[ -z "$pod_name" ]]; then
        echo -e "${RED}❌ No pod name provided${NC}"
        echo "This script should be called with a pod name as argument from k9s"
        echo -e "\n${YELLOW}Press any key to return to K9s...${NC}"
        read -n 1 -s
        exit 1
    fi
    
    echo -e "${GREEN}📊 Analyzing scheduling decision for: $pod_name${NC}\n"
    
    # Analyze the pod's scheduling decision
    analyze_pod_decision "$pod_name" "$scheduler_pod"
    
    echo -e "\n${BOLD}${CYAN}📝 Usage:${NC}"
    echo "This shows exactly how Chronos evaluated nodes for your specific pod"
    echo "• Higher scores = better fit for the workload"
    echo "• Strategy shows the scheduling approach used"
    
    echo -e "\n${YELLOW}Press any key to return to K9s...${NC}"
    read -n 1 -s
}

main "$@"
