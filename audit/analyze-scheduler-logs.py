#!/usr/bin/env python3
"""
Chronos Scheduler Log Analyzer
==============================

Analyzes Kubernetes Chronos scheduler logs and provides detailed insights into
scheduling decisions, node evaluations, and performance patterns.

Part of the fastest-empty-node-scheduler (Chronos) project.

Usage:
    python3 audit/analyze-scheduler-logs.py <log_file_path>
    
Example:
    python3 audit/analyze-scheduler-logs.py /path/to/scheduler-logs
    python3 audit/analyze-scheduler-logs.py ./scheduler.log

Features:
- Parses Chronos scheduler scoring decisions
- Extracts BIN-PACKING, EXTENSION, and EMPTY NODE strategies  
- Shows node completion times and raw/normalized scores
- Outputs structured JSON format for each pod scheduling decision
- Provides cluster-wide scheduling pattern analysis
- Saves full analysis to timestamped JSON file

Author: Chronos Scheduler Team
License: MIT
"""

import re
import json
import sys
from collections import defaultdict
from datetime import datetime

def parse_scheduler_logs(log_file_path):
    """Parse scheduler logs and extract scheduling decisions."""
    
    # Storage for scheduling sessions
    scheduling_sessions = {}
    successful_bindings = {}
    
    # Regex patterns
    patterns = {
        'scoring_start': r'Scoring pod ([^ ]+) for node (.+)',
        'timestamp': r'^I(\d{4} \d{2}:\d{2}:\d{2}\.\d+)',
        'strategy_bin_packing': r'Node ([^:]+): BIN-PACKING - NewJob=(\d+)s fits in Existing=(\d+)s.*Final=(-?\d+)',
        'strategy_extension': r'Node ([^:]+): EXTENSION - NewJob=(\d+)s > Existing=(\d+)s, Extension=(\d+)s.*Final=(-?\d+)',
        'strategy_empty': r'Node ([^:]+): EMPTY NODE.*Final=(-?\d+)',
        'final_score': r'Pod: ([^,]+), Node: ([^,]+), RawScore: (-?\d+), NormalizedScore: (\d+)',
        'successful_binding': r'Successfully bound pod to node.*pod="([^"]+)".*node="([^"]+)"',
        'pod_duration': r'NewJob=(\d+)s'
    }
    
    current_pod = None
    current_timestamp = None
    
    print("🔍 Analyzing scheduler logs...")
    
    with open(log_file_path, 'r') as file:
        for line_num, line in enumerate(file, 1):
            line = line.strip()
            
            # Extract timestamp from log line
            timestamp_match = re.search(patterns['timestamp'], line)
            if timestamp_match:
                current_timestamp = timestamp_match.group(1)
            
            # Check for successful bindings
            binding_match = re.search(patterns['successful_binding'], line)
            if binding_match:
                pod_name = binding_match.group(1)
                chosen_node = binding_match.group(2)
                successful_bindings[pod_name] = chosen_node
                continue
            
            # Check for scoring start (new pod being evaluated)
            scoring_match = re.search(patterns['scoring_start'], line)
            if scoring_match:
                current_pod = scoring_match.group(1)
                if current_pod not in scheduling_sessions:
                    scheduling_sessions[current_pod] = {
                        'pod_name': current_pod,
                        'pod_duration': None,
                        'nodes': {},
                        'chosen_node': None,
                        'timestamp': current_timestamp
                    }
                continue
            
            if not current_pod:
                continue
                
            # Extract pod duration from first strategy line
            if scheduling_sessions[current_pod]['pod_duration'] is None:
                duration_match = re.search(patterns['pod_duration'], line)
                if duration_match:
                    scheduling_sessions[current_pod]['pod_duration'] = f"{duration_match.group(1)}s"
            
            # Parse strategy lines
            for strategy, pattern in [
                ('BIN-PACKING', patterns['strategy_bin_packing']),
                ('EXTENSION', patterns['strategy_extension']),
                ('EMPTY NODE', patterns['strategy_empty'])
            ]:
                match = re.search(pattern, line)
                if match:
                    node_name = match.group(1)
                    
                    if strategy == 'BIN-PACKING':
                        completion_time = f"{match.group(3)}s"
                        raw_score = int(match.group(4))
                        details = {
                            'new_job_duration': f"{match.group(2)}s",
                            'existing_completion': completion_time,
                            'fits_in_existing': True
                        }
                    elif strategy == 'EXTENSION':
                        completion_time = f"{match.group(3)}s" 
                        raw_score = int(match.group(5))
                        details = {
                            'new_job_duration': f"{match.group(2)}s",
                            'existing_completion': completion_time,
                            'extension_needed': f"{match.group(4)}s",
                            'fits_in_existing': False
                        }
                    else:  # EMPTY NODE
                        completion_time = "0s"
                        raw_score = int(match.group(2))
                        details = {
                            'existing_completion': completion_time,
                            'is_empty': True
                        }
                    
                    scheduling_sessions[current_pod]['nodes'][node_name] = {
                        'completion_time': completion_time,
                        'strategy': strategy,
                        'raw_score': raw_score,
                        'details': details
                    }
                    break
            
            # Parse final normalized scores
            final_match = re.search(patterns['final_score'], line)
            if final_match:
                pod_name = final_match.group(1)
                node_name = final_match.group(2)
                raw_score = int(final_match.group(3))
                normalized_score = int(final_match.group(4))
                
                if pod_name in scheduling_sessions and node_name in scheduling_sessions[pod_name]['nodes']:
                    scheduling_sessions[pod_name]['nodes'][node_name]['normalized_score'] = normalized_score
    
    # Add chosen nodes from successful bindings and determine their strategies
    for pod_name, chosen_node in successful_bindings.items():
        if pod_name in scheduling_sessions:
            scheduling_sessions[pod_name]['chosen_node'] = chosen_node
            # Find the strategy used for the chosen node
            if chosen_node in scheduling_sessions[pod_name]['nodes']:
                chosen_strategy = scheduling_sessions[pod_name]['nodes'][chosen_node]['strategy']
                scheduling_sessions[pod_name]['chosen_node_strategy'] = chosen_strategy
            else:
                scheduling_sessions[pod_name]['chosen_node_strategy'] = 'UNKNOWN'
                
            # Calculate unique node counts per strategy for this pod
            pod_strategy_nodes = defaultdict(set)
            for node_name, node_data in scheduling_sessions[pod_name]['nodes'].items():
                strategy = node_data['strategy']
                pod_strategy_nodes[strategy].add(node_name)
            
            # Add strategy node counts to the pod data
            scheduling_sessions[pod_name]['strategy_node_counts'] = {
                strategy: len(nodes) for strategy, nodes in pod_strategy_nodes.items()
            }
    
    print(f"✅ Found {len(scheduling_sessions)} scheduling sessions")
    print(f"✅ Found {len(successful_bindings)} successful bindings")
    
    return scheduling_sessions

def generate_analysis_report(scheduling_sessions):
    """Generate comprehensive analysis report."""
    
    if not scheduling_sessions:
        print("❌ No scheduling sessions found!")
        return
    
    print(f"\n📊 SCHEDULER PERFORMANCE ANALYSIS")
    print(f"=" * 60)
    
    # Overall statistics
    total_sessions = len(scheduling_sessions)
    bound_sessions = sum(1 for s in scheduling_sessions.values() if s['chosen_node'])
    
    print(f"📈 Total Scheduling Sessions: {total_sessions}")
    print(f"✅ Successfully Bound: {bound_sessions}")
    print(f"❌ Failed to Bind: {total_sessions - bound_sessions}")
    
    # Strategy distribution
    strategy_counts = defaultdict(int)
    chosen_strategy_counts = defaultdict(int)
    node_utilization = defaultdict(int)
    strategy_nodes = defaultdict(set)  # Track unique nodes per strategy
    
    for session in scheduling_sessions.values():
        if session['chosen_node']:
            node_utilization[session['chosen_node']] += 1
            
        # Count chosen node strategies
        if session.get('chosen_node_strategy'):
            chosen_strategy_counts[session['chosen_node_strategy']] += 1
            
        for node_name, node_data in session['nodes'].items():
            strategy = node_data['strategy']
            strategy_counts[strategy] += 1
            strategy_nodes[strategy].add(node_name)  # Track unique nodes per strategy
    
    print(f"\n🎯 Strategy Distribution (All Evaluations):")
    for strategy, count in sorted(strategy_counts.items()):
        node_count = len(strategy_nodes[strategy])
        print(f"   {strategy}: {count} evaluations ({node_count} unique nodes)")
    
    print(f"\n🏆 Chosen Node Strategies (Successful Bindings):")
    for strategy, count in sorted(chosen_strategy_counts.items()):
        print(f"   {strategy}: {count} pods")
    
    print(f"\n🏗️ Node Utilization (Top 10):")
    for node, count in sorted(node_utilization.items(), key=lambda x: x[1], reverse=True)[:10]:
        print(f"   {node}: {count} pods")
    
    print(f"\n" + "=" * 60)
    


def main():
    """Main function."""
    if len(sys.argv) != 2:
        print("Usage: python3 audit/analyze-scheduler-logs.py <log_file_path>")
        print("Example: python3 audit/analyze-scheduler-logs.py scheduler-logs")
        print("Example: python3 audit/analyze-scheduler-logs.py /path/to/scheduler.log")
        sys.exit(1)
    
    log_file_path = sys.argv[1]
    
    try:
        # Parse logs
        scheduling_sessions = parse_scheduler_logs(log_file_path)
        
        # Generate analysis
        generate_analysis_report(scheduling_sessions)
        
        # Save full JSON output
        timestamp = datetime.now().strftime('%Y%m%d_%H%M%S')
        output_file = f"scheduler_analysis_{timestamp}.json"
        with open(output_file, 'w') as f:
            json.dump(scheduling_sessions, f, indent=2)
        
        print(f"\n💾 Full analysis saved to: {output_file}")
        
        # Create separate JSON files by chosen strategy
        strategy_files = {}
        for strategy in ['BIN-PACKING', 'EXTENSION', 'EMPTY NODE']:
            strategy_sessions = {
                pod_name: session for pod_name, session in scheduling_sessions.items()
                if session.get('chosen_node_strategy') == strategy
            }
            
            if strategy_sessions:  # Only create file if there are pods for this strategy
                strategy_file = f"scheduler_analysis_{strategy.lower().replace(' ', '_').replace('-', '_')}_{timestamp}.json"
                strategy_files[strategy] = strategy_file
                
                with open(strategy_file, 'w') as f:
                    json.dump(strategy_sessions, f, indent=2)
                
                print(f"📊 {strategy} pods ({len(strategy_sessions)}): {strategy_file}")
        
        # Summary of strategy files created
        total_pods_in_files = sum(len(json.load(open(f))) for f in strategy_files.values())
        print(f"\n🎯 Strategy-specific files created: {len(strategy_files)}")
        print(f"📋 Total pods in strategy files: {total_pods_in_files}")
        
    except FileNotFoundError:
        print(f"❌ Error: Log file '{log_file_path}' not found!")
        sys.exit(1)
    except Exception as e:
        print(f"❌ Error analyzing logs: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()
