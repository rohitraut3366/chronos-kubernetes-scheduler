#!/usr/bin/env python3
"""
Chronos Scheduler Log Parser
Analyzes scheduler logs to understand placement decisions and provide insights.
"""

import re
import sys
import json
from collections import defaultdict, namedtuple
from datetime import datetime
from typing import Dict, List, Optional, Tuple

# Data structures for parsed log entries
ChronosScore = namedtuple('ChronosScore', [
    'timestamp', 'node', 'strategy', 'pod_duration', 'existing_work', 
    'extension_duration', 'completion_time', 'final_score'
])

NormalizedScore = namedtuple('NormalizedScore', [
    'timestamp', 'pod_namespace', 'pod_name', 'node', 'raw_score', 'normalized_score'
])

BatchEvent = namedtuple('BatchEvent', [
    'timestamp', 'event_type', 'details'
])

PodBinding = namedtuple('PodBinding', [
    'timestamp', 'pod_namespace', 'pod_name', 'node', 'success'
])

class SchedulerLogParser:
    """Parses Chronos scheduler logs and provides insights"""
    
    def __init__(self):
        # Log patterns for parsing
        self.patterns = {
            # CHRONOS_SCORE: Node=worker-1, Strategy=BIN-PACKING, NewPodDuration=60s, ExistingWork=120s, ExtensionDuration=0s, CompletionTime=120s, FinalScore=1012000
            'chronos_score': re.compile(r'CHRONOS_SCORE: Node=(\S+), Strategy=(\S+), NewPodDuration=(\d+)s, ExistingWork=(\d+)s, ExtensionDuration=(\d+)s, CompletionTime=(\d+)s, FinalScore=(\d+)'),
            
            # Pod: default/test-pod, Node: worker-1, RawScore: 1012000, NormalizedScore: 100
            'normalized_score': re.compile(r'Pod: (\S+)/(\S+), Node: (\S+), RawScore: (\d+), NormalizedScore: (\d+)'),
            
            # Successfully bound pod "default/test-pod" to node="worker-1" (batch mode)
            'successful_bind': re.compile(r'Successfully bound pod "(\S+)/(\S+)" to node="(\S+)"'),
            
            # 📦 Starting batch of 5 pending pods
            'batch_start': re.compile(r'📦 Starting batch of (\d+) pending pods'),
            
            # ✅ Batch completed. Scheduled: 3, Bind-Failed: 1, Unassigned: 1.
            'batch_complete': re.compile(r'✅ Batch completed\. Scheduled: (\d+), Bind-Failed: (\d+), Unassigned: (\d+)'),
            
            # 🚀 Chronos BATCH MODE enabled - High-throughput scheduling active
            'batch_mode_enabled': re.compile(r'🚀 Chronos BATCH MODE enabled'),
            
            # 📝 Chronos INDIVIDUAL MODE enabled (default)
            'individual_mode': re.compile(r'📝 Chronos INDIVIDUAL MODE enabled'),
        }
        
        # Parsed data storage
        self.chronos_scores = []
        self.normalized_scores = []
        self.batch_events = []
        self.pod_bindings = []
        self.scheduling_mode = None
        
    def parse_timestamp(self, line: str) -> Optional[str]:
        """Extract timestamp from log line"""
        # Look for Kubernetes log timestamp format: I1025 10:30:45.123456
        ts_match = re.match(r'^[IWEF]\d{4} \d{2}:\d{2}:\d{2}\.\d{6}', line)
        if ts_match:
            return ts_match.group(0)
        return None
    
    def parse_line(self, line: str) -> None:
        """Parse a single log line"""
        timestamp = self.parse_timestamp(line)
        
        # Parse CHRONOS_SCORE entries
        if match := self.patterns['chronos_score'].search(line):
            score_entry = ChronosScore(
                timestamp=timestamp,
                node=match.group(1),
                strategy=match.group(2),
                pod_duration=int(match.group(3)),
                existing_work=int(match.group(4)),
                extension_duration=int(match.group(5)),
                completion_time=int(match.group(6)),
                final_score=int(match.group(7))
            )
            self.chronos_scores.append(score_entry)
        
        # Parse normalized score entries
        elif match := self.patterns['normalized_score'].search(line):
            norm_entry = NormalizedScore(
                timestamp=timestamp,
                pod_namespace=match.group(1),
                pod_name=match.group(2),
                node=match.group(3),
                raw_score=int(match.group(4)),
                normalized_score=int(match.group(5))
            )
            self.normalized_scores.append(norm_entry)
        
        # Parse successful bindings
        elif match := self.patterns['successful_bind'].search(line):
            binding = PodBinding(
                timestamp=timestamp,
                pod_namespace=match.group(1),
                pod_name=match.group(2),
                node=match.group(3),
                success=True
            )
            self.pod_bindings.append(binding)
        
        # Parse batch events
        elif match := self.patterns['batch_start'].search(line):
            event = BatchEvent(
                timestamp=timestamp,
                event_type='batch_start',
                details={'pod_count': int(match.group(1))}
            )
            self.batch_events.append(event)
        
        elif match := self.patterns['batch_complete'].search(line):
            event = BatchEvent(
                timestamp=timestamp,
                event_type='batch_complete',
                details={
                    'scheduled': int(match.group(1)),
                    'bind_failed': int(match.group(2)),
                    'unassigned': int(match.group(3))
                }
            )
            self.batch_events.append(event)
        
        # Parse mode detection
        elif self.patterns['batch_mode_enabled'].search(line):
            self.scheduling_mode = 'BATCH'
        elif self.patterns['individual_mode'].search(line):
            self.scheduling_mode = 'INDIVIDUAL'
    
    def parse_file(self, filepath: str) -> None:
        """Parse an entire log file"""
        print(f"📖 Parsing scheduler logs from: {filepath}")
        
        try:
            with open(filepath, 'r') as f:
                for line_num, line in enumerate(f, 1):
                    self.parse_line(line.strip())
                    
                    # Progress indicator for large files
                    if line_num % 10000 == 0:
                        print(f"   Processed {line_num:,} lines...")
                        
        except FileNotFoundError:
            print(f"❌ Error: Log file not found: {filepath}")
            sys.exit(1)
        except Exception as e:
            print(f"❌ Error parsing log file: {e}")
            sys.exit(1)
    
    def analyze_placement_decisions(self) -> Dict:
        """Analyze why specific nodes were chosen"""
        
        # Group scores by pods (approximate matching by timing)
        placement_decisions = []
        
        # Match normalized scores with CHRONOS_SCORE entries
        for norm_score in self.normalized_scores:
            pod_key = f"{norm_score.pod_namespace}/{norm_score.pod_name}"
            
            # Find corresponding CHRONOS_SCORE entries (same node, close in time)
            related_scores = [
                cs for cs in self.chronos_scores 
                if cs.node == norm_score.node
            ]
            
            if related_scores:
                # Get the most recent score for this node
                best_score = max(related_scores, key=lambda x: x.final_score)
                
                decision = {
                    'pod': pod_key,
                    'chosen_node': norm_score.node,
                    'strategy': best_score.strategy,
                    'pod_duration': best_score.pod_duration,
                    'existing_work': best_score.existing_work,
                    'extension_duration': best_score.extension_duration,
                    'raw_score': norm_score.raw_score,
                    'normalized_score': norm_score.normalized_score,
                    'reasoning': self._explain_placement_decision(best_score)
                }
                placement_decisions.append(decision)
        
        return placement_decisions
    
    def _explain_placement_decision(self, score: ChronosScore) -> str:
        """Generate human-readable explanation for placement decision"""
        
        if score.strategy == 'BIN-PACKING':
            if score.existing_work > 0:
                return f"Bin-packed: {score.pod_duration}s job fits within existing {score.existing_work}s workload (no cluster extension)"
            else:
                return f"Bin-packed: {score.pod_duration}s job on node with {score.existing_work}s existing work"
                
        elif score.strategy == 'EXTENSION':
            return f"Extension: {score.pod_duration}s job extends node completion by {score.extension_duration}s (from {score.existing_work}s to {score.completion_time}s)"
            
        elif score.strategy == 'EMPTY-NODE':
            return f"Empty node: {score.pod_duration}s job on empty node (heavily penalized but may be only option)"
            
        return f"Unknown strategy: {score.strategy}"
    
    def analyze_batch_performance(self) -> Dict:
        """Analyze batch scheduling performance"""
        
        batch_cycles = []
        
        for i, event in enumerate(self.batch_events):
            if event.event_type == 'batch_start':
                # Look for corresponding completion
                completion = None
                for j in range(i + 1, min(i + 10, len(self.batch_events))):
                    if self.batch_events[j].event_type == 'batch_complete':
                        completion = self.batch_events[j]
                        break
                
                cycle = {
                    'start_time': event.timestamp,
                    'pod_count': event.details['pod_count'],
                    'completion': completion.details if completion else None
                }
                batch_cycles.append(cycle)
        
        return {
            'total_cycles': len(batch_cycles),
            'cycles': batch_cycles,
            'avg_pods_per_batch': sum(c['pod_count'] for c in batch_cycles) / len(batch_cycles) if batch_cycles else 0
        }
    
    def generate_node_utilization_report(self) -> Dict:
        """Generate report on node utilization patterns"""
        
        node_assignments = defaultdict(int)
        node_strategies = defaultdict(lambda: defaultdict(int))
        
        for binding in self.pod_bindings:
            node_assignments[binding.node] += 1
        
        for score in self.chronos_scores:
            node_strategies[score.node][score.strategy] += 1
        
        return {
            'assignments_per_node': dict(node_assignments),
            'strategies_per_node': {node: dict(strategies) for node, strategies in node_strategies.items()},
            'total_assignments': sum(node_assignments.values())
        }
    
    def print_pod_decision_analysis(self, pod_name: str = None) -> None:
        """Print detailed analysis for specific pod (k9s plugin style)"""
        
        if not pod_name:
            # Show all pods
            decisions = self.analyze_placement_decisions()
            if not decisions:
                print("❌ No pod scheduling decisions found in logs")
                return
            
            for decision in decisions[:5]:  # Show first 5 in detail
                self._print_single_pod_analysis(decision)
                print()
        else:
            # Show specific pod
            decisions = [d for d in self.analyze_placement_decisions() if pod_name in d['pod']]
            if not decisions:
                print(f"❌ No scheduling decision found for pod: {pod_name}")
                return
            
            self._print_single_pod_analysis(decisions[0])
    
    def _print_single_pod_analysis(self, decision: dict) -> None:
        """Print k9s plugin style analysis for a single pod"""
        
        pod = decision['pod']
        chosen_node = decision['chosen_node']
        strategy = decision['strategy']
        reasoning = decision['reasoning']
        
        print(f"┌{'─' * 80}┐")
        print(f"│ {'🎯 CHRONOS SCHEDULING DECISION':^78} │")
        print(f"├{'─' * 80}┤")
        print(f"│ Pod Name: {pod:65} │")
        print(f"│ Chosen Node: {chosen_node:62} │")
        print(f"│ Strategy: {strategy:67} │")
        print(f"└{'─' * 80}┘")
        
        # Find all scoring entries for this pod's scheduling decision
        pod_scores = []
        for score in self.chronos_scores:
            # Group scores that happened around the same time
            # This is approximate but works for most cases
            pod_scores.append(score)
        
        if not pod_scores:
            print("⚠️  No detailed scoring information available")
            return
        
        # Create table similar to k9s plugin
        print(f"\n🎯 NODE EVALUATION DETAILS:")
        
        # Table header
        print("┌─────────────────┬─────────────┬─────────────────┬─────────────┬─────────────┬───────────────────────┐")
        print("│ Node Name       │ Strategy    │ Completion Time │ Raw Score   │ Norm Score  │ Calculation Details   │")
        print("├─────────────────┼─────────────┼─────────────────┼─────────────┼─────────────┼───────────────────────┤")
        
        # Sort by score (highest first)
        pod_scores.sort(key=lambda x: x.final_score, reverse=True)
        
        for score in pod_scores[:8]:  # Show top 8 nodes
            node = score.node
            strat = score.strategy
            completion = f"{score.completion_time}s"
            raw_score = f"{score.final_score:,}"
            
            # Find normalized score
            norm_score = "N/A"
            for norm in self.normalized_scores:
                if norm.node == node:
                    norm_score = str(norm.normalized_score)
                    break
            
            # Create calculation details
            if strat == "BIN-PACKING":
                calc_details = f"Fits in {score.existing_work}s"
                marker = "✅" if node == chosen_node else "  "
            elif strat == "EXTENSION":
                calc_details = f"Extends {score.extension_duration}s"
                marker = "✅" if node == chosen_node else "  "
            elif strat == "EMPTY-NODE":
                calc_details = "Empty node"
                marker = "✅" if node == chosen_node else "  "
            else:
                calc_details = "Unknown"
                marker = "  "
            
            # Format strategy display
            display_strategy = strat.replace("EMPTY-NODE", "EMPTY NODE")
            
            print(f"│ {marker}{node:<14} │ {display_strategy:<11} │ {completion:<15} │ {raw_score:<11} │ {norm_score:<11} │ {calc_details:<21} │")
        
        print("└─────────────────┴─────────────┴─────────────────┴─────────────┴─────────────┴───────────────────────┘")
        
        # Winner details
        winner_score = next((s for s in pod_scores if s.node == chosen_node), None)
        if winner_score:
            print(f"\n✅ CHOSEN NODE: {chosen_node}")
            print(f"🎯 Strategy Used: {winner_score.strategy.replace('EMPTY-NODE', 'EMPTY NODE')}")
            print(f"📊 Detailed Calculation:")
            print(f"   • New Job Duration: {winner_score.pod_duration}s")
            print(f"   • Existing Work: {winner_score.existing_work}s")
            if winner_score.extension_duration > 0:
                print(f"   • Extension Duration: {winner_score.extension_duration}s")
            print(f"   • Final Score: {winner_score.final_score:,}")
        
        print(f"\n💡 Decision Reasoning: {reasoning}")
        
    def print_summary_report(self) -> None:
        """Print comprehensive analysis report"""
        
        print("\n" + "="*80)
        print("🎯 CHRONOS SCHEDULER LOG ANALYSIS REPORT")
        print("="*80)
        
        # Basic stats
        print(f"📊 PARSING SUMMARY:")
        print(f"  • Scheduling Mode: {self.scheduling_mode or 'Unknown'}")
        print(f"  • CHRONOS_SCORE entries: {len(self.chronos_scores):,}")
        print(f"  • Normalized score entries: {len(self.normalized_scores):,}")
        print(f"  • Pod bindings: {len(self.pod_bindings):,}")
        print(f"  • Batch events: {len(self.batch_events):,}")
        
        # Show detailed pod decisions (k9s style)
        if self.normalized_scores:
            print(f"\n" + "="*80)
            print("📋 DETAILED SCHEDULING DECISIONS (K9s Plugin Style)")
            print("="*80)
            
            self.print_pod_decision_analysis()
            
            # Strategy summary
            decisions = self.analyze_placement_decisions()
            strategy_counts = defaultdict(int)
            for decision in decisions:
                strategy_counts[decision['strategy']] += 1
            
            print(f"\n📈 STRATEGY DISTRIBUTION:")
            for strategy, count in strategy_counts.items():
                icon = "🎯" if strategy == "BIN-PACKING" else "🔄" if strategy == "EXTENSION" else "📦"
                print(f"  {icon} {strategy}: {count} pods")
        
        # Batch performance
        if self.scheduling_mode == 'BATCH' and self.batch_events:
            print(f"\n🚀 BATCH SCHEDULING PERFORMANCE:")
            batch_perf = self.analyze_batch_performance()
            print(f"  • Total batch cycles: {batch_perf['total_cycles']}")
            print(f"  • Avg pods per batch: {batch_perf['avg_pods_per_batch']:.1f}")
            
            for cycle in batch_perf['cycles'][:5]:  # Show first 5 cycles
                if cycle['completion']:
                    comp = cycle['completion']
                    success_rate = comp['scheduled'] / (comp['scheduled'] + comp['bind_failed'] + comp['unassigned']) * 100
                    print(f"  📦 Batch: {cycle['pod_count']} pods → ✅ {comp['scheduled']}, ❌ {comp['bind_failed']}, ⏸️  {comp['unassigned']} ({success_rate:.1f}% success)")
        
        # Node utilization with visual representation
        print(f"\n🏗️ NODE UTILIZATION ANALYSIS:")
        node_report = self.generate_node_utilization_report()
        
        max_assignments = max(node_report['assignments_per_node'].values()) if node_report['assignments_per_node'] else 1
        
        for node, count in sorted(node_report['assignments_per_node'].items()):
            strategies = node_report['strategies_per_node'].get(node, {})
            strategy_summary = ", ".join([f"{s}: {c}" for s, c in strategies.items()])
            
            # Visual bar
            bar_length = int((count / max_assignments) * 20)
            bar = "█" * bar_length + "░" * (20 - bar_length)
            
            print(f"  {node:<20} {bar} {count:3d} pods ({strategy_summary})")
        
        print(f"\n📝 LEGEND:")
        print(f"  🎯 BIN-PACKING = Job fits in existing completion time")
        print(f"  🔄 EXTENSION = Job extends node completion time")
        print(f"  📦 EMPTY NODE = Node has no running jobs")
        
        print(f"\n💡 INSIGHTS:")
        print(f"  • Total pod placements analyzed: {len(self.pod_bindings)}")
        print(f"  • Scheduler appears to be working in {self.scheduling_mode or 'Unknown'} mode")
        
        if self.scheduling_mode == 'BATCH':
            print(f"  • 🚀 Batch scheduling is processing pods in groups for better performance")
        else:
            print(f"  • 📝 Individual scheduling is processing pods one by one")

def main():
    if len(sys.argv) != 2:
        print("Usage: python3 parse-scheduler-logs.py <scheduler-log-file>")
        print("\nExample:")
        print("  kubectl logs -n chronos-system deployment/chronos-scheduler > scheduler.log")
        print("  python3 parse-scheduler-logs.py scheduler.log")
        sys.exit(1)
    
    log_file = sys.argv[1]
    
    # Parse the logs
    parser = SchedulerLogParser()
    parser.parse_file(log_file)
    
    # Generate analysis report
    parser.print_summary_report()

if __name__ == "__main__":
    main()
