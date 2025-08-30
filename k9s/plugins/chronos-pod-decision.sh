#!/bin/bash
# K9s Plugin: Show Chronos scheduling decision for specific pod
# Usage: Called from K9s when Ctrl-D is pressed on a pod

set +e  # Don't exit on error - let user control exit

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
    local max_total_logs="0"
    
    while IFS= read -r pod; do
        if [[ -n "$pod" ]]; then
            local namespace=$(echo "$pod" | cut -d'/' -f1)
            local pod_name=$(echo "$pod" | cut -d'/' -f2)
            
            # Quick check: look for recent scheduling activity (last 50 lines) instead of full logs
            # This is much faster than scanning entire log history
            local total_logs
            total_logs=$(kubectl logs -n "$namespace" "$pod_name" --tail=50 2>/dev/null | grep -c -E "(Successfully bound|Attempting to schedule|CHRONOS_SCORE)" 2>/dev/null || echo 0)
            # Clean up whitespace and ensure it's a valid number
            total_logs=$(echo "$total_logs" | tr -d '\n\r' | tr -d ' ' | grep '^[0-9]*$' || echo 0)
            
            echo -e "${CYAN}  $pod: $total_logs recent scheduling events${NC}"
            
            if [[ "$total_logs" -gt "$max_total_logs" ]] 2>/dev/null; then
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
    local pod_namespace="$3"
    local scheduler_namespace=$(echo "$scheduler_pod" | cut -d'/' -f1)
    local scheduler_pod_name_only=$(echo "$scheduler_pod" | cut -d'/' -f2)
    
    echo -e "${BOLD}${CYAN}Chronos Scheduling Decision for Pod: ${GREEN}$pod_name${NC}\n"
    
    # Get scheduler logs and filter for this specific pod
    local logs
    logs=$(kubectl logs -n "$scheduler_namespace" "$scheduler_pod_name_only" 2>/dev/null | grep -E "$pod_name" || true)
    
    # Pre-extract commonly used log patterns for performance
    local chronos_score_logs=$(echo "$logs" | grep "CHRONOS_SCORE:" || true)
    local bound_logs=$(echo "$logs" | grep "Successfully bound pod" || true)
    
    if [[ -z "$logs" ]]; then
        echo -e "${RED}❌ No scheduling logs found for pod: $pod_name${NC}"
        echo -e "\n${BOLD}${YELLOW}🔍 Most likely reasons:${NC}"
        echo "• 🔄 Logs have rotated out - try more recent pods"
        echo "• 📋 Pod was not scheduled by Chronos (using default scheduler instead)"
        echo "• ⏳ Pod is still pending and hasn't been processed yet"
        echo "• 🔄 Scheduler restarted recently (logs cleared)"
        echo "• 📅 Pod missing required duration annotation (Chronos skipped it)"
        
        echo -e "\n${BOLD}${CYAN}🛠️  What to check:${NC}"
        echo "1. Verify pod details:"
        echo "   kubectl get pod $pod_name -n $pod_namespace -o yaml | head -20"
        echo ""
        echo "2. Check scheduler name:"
        echo "   kubectl get pod $pod_name -n $pod_namespace -o jsonpath='{.spec.schedulerName}'"
        echo ""
        echo "3. Look for duration annotation:"
        echo "   kubectl get pod $pod_name -n $pod_namespace -o jsonpath='{.metadata.annotations.scheduling\.workload\.io/expected-duration-seconds}'"
        echo ""
        echo "4. Check pod status and events:"
        echo "   kubectl describe pod $pod_name -n $pod_namespace | tail -20"
        
        return 1
    fi
    
    echo -e "${BLUE}📊 Found scheduling data for pod${NC}\n"
    
    # Parse the scheduling session
    local pod_duration=""
    local chosen_node=""
    local chosen_strategy=""
    local timestamp=""
    
    # Extract basic info - try multiple methods to get pod duration
    pod_duration=""
    
    # Method 1: Try to extract from CHRONOS_SCORE logs (NewJobDuration or NewPodDuration)
    pod_duration=$(echo "$chronos_score_logs" | head -1 | sed -n 's/.*NewJobDuration=\([^,]*\),.*/\1/p')
    [[ -z "$pod_duration" ]] && pod_duration=$(echo "$chronos_score_logs" | head -1 | sed -n 's/.*NewPodDuration=\([^,]*\),.*/\1/p')
    
    # Method 2: Try old format as fallback
    [[ -z "$pod_duration" ]] && pod_duration=$(echo "$logs" | grep -o "NewJob=[0-9]*s" | head -1 | cut -d'=' -f2)
    
    # Method 3: Try to get from pod annotations
    if [[ -z "$pod_duration" || "$pod_duration" == "N/A" ]]; then
        pod_duration=$(kubectl get pod "$pod_name" -n "$pod_namespace" -o jsonpath='{.metadata.annotations.scheduling\.workload\.io/expected-duration-seconds}' 2>/dev/null)
        [[ -n "$pod_duration" && "$pod_duration" != "null" ]] && pod_duration="${pod_duration}s (from annotation)" || pod_duration="N/A"
    fi
    chosen_node=$(echo "$bound_logs" | grep "$pod_name" | sed -n 's/.*node="\([^"]*\)".*/\1/p' || echo "N/A")
    timestamp=$(echo "$logs" | head -1 | grep -o '^I[0-9]* [0-9:]*\.[0-9]*' | tr -d 'I' || echo "N/A")
    
    # Count nodes evaluated from CHRONOS_SCORE logs
    local nodes_evaluated=$(echo "$chronos_score_logs" | wc -l | tr -d ' ')
    
    # Show key scheduling details
    echo -e "${BOLD}${CYAN}📋 SCHEDULING DECISION SUMMARY${NC}"
    echo "════════════════════════════════════════════════════════════════════════════════"
    echo -e "${BOLD}$(printf '%-20s' "Pod Name:")${NC} ${GREEN}$pod_name${NC}"
    echo -e "${BOLD}$(printf '%-20s' "Pod Duration:")${NC} ${YELLOW}$pod_duration${NC}"
    echo -e "${BOLD}$(printf '%-20s' "Chosen Node:")${NC} ${GREEN}$chosen_node${NC}"
    echo -e "${BOLD}$(printf '%-20s' "Scheduled At:")${NC} ${BLUE}$timestamp${NC}"
    echo -e "${BOLD}$(printf '%-20s' "Nodes Evaluated:")${NC} ${CYAN}$nodes_evaluated${NC}"
    
    echo "════════════════════════════════════════════════════════════════════════════════"
    
    echo -e "\n${BOLD}🎯 NODE EVALUATION DETAILS:${NC}"
    
    # First pass: collect all data and calculate column widths
    local node_data=()
    local max_node_width=9  # minimum for "Node Name"
    local max_strategy_width=8  # minimum for "Strategy" 
    local max_completion_width=15  # minimum for "Completion Time"
    local max_raw_width=9  # minimum for "Raw Score"
    local max_calc_width=19  # minimum for "Calculation Details"
    
    # Parse node evaluations from detailed CHRONOS_SCORE format - optimized for performance
    while IFS= read -r line; do
        # Optimized parsing - extract all values in fewer operations
        local node_name=$(echo "$line" | sed -n 's/.*Node=\([^,]*\),.*/\1/p')
        local strategy=$(echo "$line" | sed -n 's/.*Strategy=\([^,]*\),.*/\1/p')
        
        # Try NewJobDuration first, then NewPodDuration as fallback
        local new_job_duration=$(echo "$line" | sed -n 's/.*NewJobDuration=\([^,]*\),.*/\1/p')
        [[ -z "$new_job_duration" ]] && new_job_duration=$(echo "$line" | sed -n 's/.*NewPodDuration=\([^,]*\),.*/\1/p')
        
        # Try ExistingWork first, then maxRemainingTime as fallback  
        local existing_work=$(echo "$line" | sed -n 's/.*ExistingWork=\([^,]*\),.*/\1/p')
        [[ -z "$existing_work" ]] && existing_work=$(echo "$line" | sed -n 's/.*maxRemainingTime=\([^,]*\),.*/\1/p')
        
        local extension_duration=$(echo "$line" | sed -n 's/.*ExtensionDuration=\([^,]*\),.*/\1/p')
        local completion_time=$(echo "$line" | sed -n 's/.*CompletionTime=\([^,]*\),.*/\1/p')
        local raw_score=$(echo "$line" | sed -n 's/.*FinalScore=\([0-9-]*\).*/\1/p')
        
        # Create detailed calculation info for display
        if [[ "$strategy" == "BIN-PACKING" ]]; then
            calc_info="Fits in ${existing_work}"
        elif [[ "$strategy" == "EXTENSION" ]]; then
            calc_info="Extends ${extension_duration}"
        else
            calc_info="Empty node"
        fi
        
        # Store data for later formatting
        display_strategy="$strategy"
        [[ "$strategy" == "EMPTY-NODE" ]] && display_strategy="EMPTY NODE"
        
        node_data+=("$node_name|$display_strategy|$completion_time|$raw_score|$calc_info|$strategy")
        
        # Update maximum widths
        [[ ${#node_name} -gt $max_node_width ]] && max_node_width=${#node_name}
        [[ ${#display_strategy} -gt $max_strategy_width ]] && max_strategy_width=${#display_strategy}
        [[ ${#completion_time} -gt $max_completion_width ]] && max_completion_width=${#completion_time}
        [[ ${#raw_score} -gt $max_raw_width ]] && max_raw_width=${#raw_score}
        [[ ${#calc_info} -gt $max_calc_width ]] && max_calc_width=${#calc_info}
    done <<< "$chronos_score_logs"
    
    # Generate dynamic table borders and headers
    local border_line="┌$(printf "%*s" $((max_node_width + 2)) "" | tr ' ' '─')┬$(printf "%*s" $((max_strategy_width + 2)) "" | tr ' ' '─')┬$(printf "%*s" $((max_completion_width + 2)) "" | tr ' ' '─')┬$(printf "%*s" $((max_raw_width + 2)) "" | tr ' ' '─')┬$(printf "%*s" $((max_calc_width + 2)) "" | tr ' ' '─')┐"
    local separator_line="├$(printf "%*s" $((max_node_width + 2)) "" | tr ' ' '─')┼$(printf "%*s" $((max_strategy_width + 2)) "" | tr ' ' '─')┼$(printf "%*s" $((max_completion_width + 2)) "" | tr ' ' '─')┼$(printf "%*s" $((max_raw_width + 2)) "" | tr ' ' '─')┼$(printf "%*s" $((max_calc_width + 2)) "" | tr ' ' '─')┤"
    local bottom_line="└$(printf "%*s" $((max_node_width + 2)) "" | tr ' ' '─')┴$(printf "%*s" $((max_strategy_width + 2)) "" | tr ' ' '─')┴$(printf "%*s" $((max_completion_width + 2)) "" | tr ' ' '─')┴$(printf "%*s" $((max_raw_width + 2)) "" | tr ' ' '─')┴$(printf "%*s" $((max_calc_width + 2)) "" | tr ' ' '─')┘"
    
    # Print table header
    echo "$border_line"
    printf "│ %-*s │ %-*s │ %-*s │ %-*s │ %-*s │\n" \
        $max_node_width "Node Name" \
        $max_strategy_width "Strategy" \
        $max_completion_width "Completion Time" \
        $max_raw_width "Score" \
        $max_calc_width "Calculation Details"
    echo "$separator_line"
    
    # Second pass: format and display data
    for data in "${node_data[@]}"; do
        IFS='|' read -r node_name display_strategy completion_time raw_score calc_info strategy <<< "$data"
        
        # Highlight chosen node and color-code strategies
        if [[ "$node_name" == "$chosen_node" ]]; then
            case "$strategy" in
                "BIN-PACKING")
                    echo -e "$(printf "│ ${GREEN}%-*s${NC} │ ${CYAN}%-*s${NC} │ ${YELLOW}%-*s${NC} │ ${MAGENTA}%-*s${NC} │ ${CYAN}%-*s${NC} │" $max_node_width "$node_name" $max_strategy_width "$display_strategy" $max_completion_width "$completion_time" $max_raw_width "$raw_score" $max_calc_width "$calc_info")"
                    ;;
                "EXTENSION")
                    echo -e "$(printf "│ ${GREEN}%-*s${NC} │ ${YELLOW}%-*s${NC} │ ${YELLOW}%-*s${NC} │ ${MAGENTA}%-*s${NC} │ ${YELLOW}%-*s${NC} │" $max_node_width "$node_name" $max_strategy_width "$display_strategy" $max_completion_width "$completion_time" $max_raw_width "$raw_score" $max_calc_width "$calc_info")"
                    ;;
                "EMPTY-NODE")
                    echo -e "$(printf "│ ${GREEN}%-*s${NC} │ ${MAGENTA}%-*s${NC} │ ${YELLOW}%-*s${NC} │ ${MAGENTA}%-*s${NC} │ ${MAGENTA}%-*s${NC} │" $max_node_width "$node_name" $max_strategy_width "$display_strategy" $max_completion_width "$completion_time" $max_raw_width "$raw_score" $max_calc_width "$calc_info")"
                    ;;
                *)
                    echo -e "$(printf "│ ${GREEN}%-*s${NC} │ %-*s │ ${YELLOW}%-*s${NC} │ ${MAGENTA}%-*s${NC} │ %-*s │" $max_node_width "$node_name" $max_strategy_width "$display_strategy" $max_completion_width "$completion_time" $max_raw_width "$raw_score" $max_calc_width "$calc_info")"
                    ;;
            esac
        else
            # Non-chosen nodes
            echo "$(printf "│ %-*s │ %-*s │ %-*s │ %-*s │ %-*s │" $max_node_width "$node_name" $max_strategy_width "$display_strategy" $max_completion_width "$completion_time" $max_raw_width "$raw_score" $max_calc_width "$calc_info")"
        fi
    done
    
    echo "$bottom_line"
    
    echo -e "\n${BOLD}${GREEN}✅ Chosen Node: $chosen_node${NC}"
    
    # Get detailed calculation info for chosen node from CHRONOS_SCORE logs
    chosen_line=$(echo "$chronos_score_logs" | grep "Node=$chosen_node," | head -1)
    if [[ -n "$chosen_line" ]]; then
        chosen_strategy=$(echo "$chosen_line" | sed -n 's/.*Strategy=\([^,]*\),.*/\1/p')
        
        # Try NewJobDuration first, then NewPodDuration as fallback
        chosen_new_job=$(echo "$chosen_line" | sed -n 's/.*NewJobDuration=\([^,]*\),.*/\1/p')
        [[ -z "$chosen_new_job" ]] && chosen_new_job=$(echo "$chosen_line" | sed -n 's/.*NewPodDuration=\([^,]*\),.*/\1/p')
        
        # Try ExistingWork first, then maxRemainingTime as fallback
        chosen_existing=$(echo "$chosen_line" | sed -n 's/.*ExistingWork=\([^,]*\),.*/\1/p')
        [[ -z "$chosen_existing" ]] && chosen_existing=$(echo "$chosen_line" | sed -n 's/.*maxRemainingTime=\([^,]*\),.*/\1/p')
        
        chosen_extension=$(echo "$chosen_line" | sed -n 's/.*ExtensionDuration=\([^,]*\),.*/\1/p')
        chosen_final=$(echo "$chosen_line" | sed -n 's/.*FinalScore=\([0-9-]*\).*/\1/p')
        
        display_strategy="$chosen_strategy"
        [[ "$chosen_strategy" == "EMPTY-NODE" ]] && display_strategy="EMPTY NODE"
        echo -e "${BOLD}🎯 Strategy Used: ${CYAN}$display_strategy${NC}"
        
        echo -e "\n${BOLD}${CYAN}📊 Detailed Calculation:${NC}"
        echo -e "• New Job Duration: ${YELLOW}$chosen_new_job${NC}"
        echo -e "• Existing Work: ${BLUE}$chosen_existing${NC}"
        if [[ "$chosen_extension" != "0s" ]]; then
            echo -e "• Extension Duration: ${RED}$chosen_extension${NC}"
        fi
        echo -e "• Final Score: ${BOLD}${GREEN}$chosen_final${NC}"
    fi
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
        echo -e "\n${BOLD}${CYAN}🔧 Debug Information:${NC}"
        echo "• Check if scheduler is running:"
        echo "  kubectl get pods -n codeship-custom-scheduler-eks -l app.kubernetes.io/name=chronos-kubernetes-scheduler"
        echo "• Check if namespace exists:"
        echo "  kubectl get namespace codeship-custom-scheduler-eks"
        echo "• Check current context:"
        echo "  kubectl config current-context"
        echo -e "\n${YELLOW}Press any key to return to K9s...${NC}"
        read -n 1 -s
        return 1  # Return instead of exit to allow cleanup
    fi
    
    # Show the scheduler identification results (excluding the final line)
    echo "$scheduler_pod_output" | sed '$d'
    
    # Get pod name from argument (passed by k9s)
    local pod_name="$1"
    local namespace="$2"
    
    if [[ -z "$pod_name" ]]; then
        echo -e "${RED}❌ No pod name provided${NC}"
        echo "This script should be called with a pod name as argument from k9s"
        echo -e "\n${BOLD}${CYAN}🔧 Debug Information:${NC}"
        echo "• This script expects arguments: pod_name namespace"
        echo "• Current arguments received: '$1' '$2'"
        echo "• Make sure the k9s plugin is configured correctly"
        echo "• Check plugin configuration in: ~/.config/k9s/plugins.yaml"
        echo -e "\n${YELLOW}Press any key to return to K9s...${NC}"
        read -n 1 -s
        return 1  # Return instead of exit to allow cleanup
    fi
    
    echo -e "${GREEN}📊 Analyzing scheduling decision for: $pod_name (ns: $namespace)${NC}\n"
    
    # Analyze the pod's scheduling decision
    if ! analyze_pod_decision "$pod_name" "$scheduler_pod" "$namespace"; then
        echo -e "\n${BOLD}${CYAN}🔧 Troubleshooting Tips:${NC}"
        echo "• Pod might not have been scheduled by Chronos"
        echo "• Try checking if pod has the duration annotation:"
        echo "  kubectl get pod $pod_name -n $namespace -o jsonpath='{.metadata.annotations}'"
        echo "• Check if Chronos is the active scheduler:"
        echo "  kubectl get pod $pod_name -n $namespace -o jsonpath='{.spec.schedulerName}'"
        echo "• Look at recent scheduler logs manually:"
        echo "  kubectl logs -n codeship-custom-scheduler-eks $(echo '$scheduler_pod' | cut -d'/' -f2) | grep $pod_name"
        echo -e "\n${YELLOW}Even if analysis failed, you can still investigate manually using the commands above.${NC}"
    fi
    
    # Always show help information and exit prompt, regardless of success/failure
    echo -e "\n${BOLD}${CYAN}📝 Legend:${NC}"
    echo -e "${GREEN}• * = Chosen node${NC}"
    echo -e "${CYAN}• BIN-PACKING${NC} = Job fits in existing completion time"
    echo -e "${YELLOW}• EXTENSION${NC} = Job extends node completion time"
    echo -e "${MAGENTA}• EMPTY NODE${NC} = Node has no running jobs"
    
    echo -e "\n${BOLD}${CYAN}📝 Usage:${NC}"
    echo "This shows exactly how Chronos evaluated nodes for your specific pod"
    echo "• Higher scores = better fit for the workload"
    echo "• Strategy shows the scheduling approach used"
    
    echo -e "\n${YELLOW}Press any key to return to K9s...${NC}"
    read -n 1 -s
    
    # Always return 0 so k9s plugin doesn't show errors
    return 0
}

main "$@"
