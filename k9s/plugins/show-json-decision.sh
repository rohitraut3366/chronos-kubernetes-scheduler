#!/bin/bash

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m' # No Color

show_json_scheduling_decision() {
    local pod_name="$1"
    local json_file="$2"
    
    if [[ ! -f "$json_file" ]]; then
        echo -e "${RED}❌ Error: JSON file '$json_file' not found${NC}"
        return 1
    fi
    
    # Check if pod exists in JSON (try exact match first)
    local exists=$(jq -r "has(\"$pod_name\")" "$json_file" 2>/dev/null)
    if [[ "$exists" != "true" ]]; then
        # Try to find partial matches
        echo -e "${YELLOW}⚠️  Exact pod name not found. Searching for partial matches...${NC}"
        local matches=$(jq -r "keys[]" "$json_file" 2>/dev/null | grep -F "$pod_name" | head -5)
        if [[ -n "$matches" ]]; then
            echo -e "${CYAN}🔍 Found similar pods:${NC}"
            echo "$matches" | nl -w2 -s'. '
            echo -e "\n${YELLOW}Using first match for analysis:${NC}"
            pod_name=$(echo "$matches" | head -1)
        else
            echo -e "${RED}❌ No matching pods found for '$pod_name'${NC}"
            echo -e "${BLUE}💡 Available pods in this analysis:${NC}"
            jq -r "keys[] | select(length > 0)" "$json_file" 2>/dev/null | head -5 | nl -w2 -s'. '
            local total=$(jq -r "keys | length" "$json_file" 2>/dev/null)
            if [[ "$total" -gt 5 ]]; then
                echo "... and $((total - 5)) more"
            fi
            return 1
        fi
    fi
    
    echo -e "\n${BOLD}${BLUE}🔍 CHRONOS SCHEDULING DECISION (from JSON)${NC}"
    echo "═══════════════════════════════════════════════════════════════════════════════"
    
    # Extract pod info from JSON
    local pod_duration=$(jq -r ".[\"$pod_name\"].pod_duration // \"N/A\"" "$json_file")
    local chosen_node="N/A"
    
    # Find chosen node (highest normalized score)
    local best_score=-999999
    while IFS= read -r node; do
        local norm_score=$(jq -r ".[\"$pod_name\"].nodes[\"$node\"].normalized_score // 0" "$json_file")
        if [[ "$norm_score" -gt "$best_score" ]]; then
            best_score="$norm_score"
            chosen_node="$node"
        fi
    done < <(jq -r ".[\"$pod_name\"].nodes | keys[]" "$json_file" 2>/dev/null)
    
    # Count nodes evaluated
    local nodes_evaluated=$(jq -r ".[\"$pod_name\"].nodes | length" "$json_file")
    
    # Show key scheduling details
    echo -e "${BOLD}${CYAN}📋 SCHEDULING DECISION SUMMARY${NC}"
    echo "════════════════════════════════════════════════════════════════════════════════"
    printf "${BOLD}%-20s${NC} %s\n" "Pod Name:" "${GREEN}$pod_name${NC}"
    printf "${BOLD}%-20s${NC} %s\n" "Pod Duration:" "${YELLOW}$pod_duration${NC}"
    printf "${BOLD}%-20s${NC} %s\n" "Chosen Node:" "${GREEN}$chosen_node${NC}"
    printf "${BOLD}%-20s${NC} %s\n" "Nodes Evaluated:" "${CYAN}$nodes_evaluated${NC}"
    echo "════════════════════════════════════════════════════════════════════════════════"
    
    # Display node evaluation details from JSON
    echo -e "\n${BOLD}🎯 NODE EVALUATION DETAILS:${NC}"
    echo "┌─────────────────────────────────────┬─────────────┬─────────────────┬────────────┬────────────┐"
    echo "│ Node Name                           │ Strategy    │ Completion Time │ Raw Score  │ Norm Score │"
    echo "├─────────────────────────────────────┼─────────────┼─────────────────┼────────────┼────────────┤"
    
    # Parse nodes from JSON and sort by normalized score (descending)
    while IFS= read -r entry; do
        local node=$(echo "$entry" | cut -d'|' -f1)
        local norm_score=$(echo "$entry" | cut -d'|' -f2)
        local completion_time=$(jq -r ".[\"$pod_name\"].nodes[\"$node\"].completion_time // \"N/A\"" "$json_file")
        local strategy=$(jq -r ".[\"$pod_name\"].nodes[\"$node\"].strategy // \"N/A\"" "$json_file")
        local raw_score=$(jq -r ".[\"$pod_name\"].nodes[\"$node\"].raw_score // \"N/A\"" "$json_file")
        
        # Format large numbers with commas
        if [[ "$raw_score" =~ ^-?[0-9]+$ ]]; then
            raw_score=$(printf "%'d" "$raw_score" 2>/dev/null || echo "$raw_score")
        fi
        
        # Highlight chosen node (best score)
        if [[ "$node" == "$chosen_node" ]]; then
            printf "│ ${GREEN}%-35s${NC} │ ${CYAN}%-11s${NC} │ ${YELLOW}%-15s${NC} │ ${MAGENTA}%10s${NC} │ ${BOLD}%10s${NC} │\n" \
                "${node:0:35}" "$strategy" "$completion_time" "$raw_score" "$norm_score"
        else
            printf "│ %-35s │ %-11s │ %-15s │ %10s │ %10s │\n" \
                "${node:0:35}" "$strategy" "$completion_time" "$raw_score" "$norm_score"
        fi
    done < <(jq -r ".[\"$pod_name\"].nodes | to_entries[] | \"\(.key)|\(.value.normalized_score)\"" "$json_file" | sort -t'|' -k2 -nr)
    
    echo "└─────────────────────────────────────┴─────────────┴─────────────────┴────────────┴────────────┘"
    
    # Show strategy details for chosen node
    echo -e "\n${BOLD}🎯 CHOSEN NODE DETAILS:${NC}"
    local details=$(jq -r ".[\"$pod_name\"].nodes[\"$chosen_node\"].details" "$json_file" 2>/dev/null)
    if [[ "$details" != "null" && "$details" != "" ]]; then
        echo "$details" | jq -r 'to_entries[] | "• \(.key | gsub("_"; " ") | ascii_upcase): \(.value)"' 2>/dev/null || echo "• Details available in raw JSON"
    fi
    
    echo
}

# Main execution
if [[ $# -lt 2 ]]; then
    echo "Usage: $0 <pod_name> <json_file>"
    echo "Example: $0 'namespace/podname' scheduler_analysis.json"
    exit 1
fi

show_json_scheduling_decision "$1" "$2"
