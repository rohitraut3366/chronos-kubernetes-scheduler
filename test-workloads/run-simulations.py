#!/usr/bin/env python3
"""
Chronos Scheduler Simulation Runner
Executes realistic scheduling scenarios to validate bin-packing logic
"""

import yaml # type: ignore
import subprocess
import time
import sys
import re
from typing import Dict, List, Any, Optional

class ChronosSimulator:
    def __init__(self, config_file: str, kubeconfig: str = None):
        with open(config_file, 'r') as f:
            self.config = yaml.safe_load(f)
        self.kubeconfig = kubeconfig
        self.exec_config = self.config['execution']
        self.namespace = self.exec_config['namespace']
        
    def kubectl(self, args: List[str]) -> tuple[str, int]:
        """Execute kubectl command and return output, exit_code"""
        cmd = ['kubectl'] + args
        if self.kubeconfig:
            cmd.extend(['--kubeconfig', self.kubeconfig])
        
        print(f"🔧 Running: {' '.join(cmd)}")
        result = subprocess.run(cmd, capture_output=True, text=True)
        return result.stdout.strip(), result.returncode
    
    def create_namespace(self):
        """Create test namespace"""
        print(f"📁 Creating namespace: {self.namespace}")
        output, code = self.kubectl(['create', 'namespace', self.namespace])
        if code != 0 and 'already exists' not in output:
            print(f"❌ Failed to create namespace: {output}")
            return False
        return True
    
    def cleanup_namespace(self):
        """Clean up test namespace"""
        print(f"🧹 Cleaning up namespace: {self.namespace}")
        self.kubectl(['delete', 'namespace', self.namespace, '--ignore-not-found=true'])
        cleanup_wait = self.exec_config.get('cleanup_wait', 5)
        time.sleep(cleanup_wait)  # Wait for cleanup
    
    def get_nodes(self) -> List[str]:
        """Get available worker nodes"""
        output, code = self.kubectl(['get', 'nodes', '-o', 'jsonpath={.items[*].metadata.name}'])
        if code != 0:
            print(f"❌ Failed to get nodes: {output}")
            return []
        
        nodes = output.split()
        # Filter out control-plane nodes
        worker_nodes = [node for node in nodes if 'control-plane' not in node]
        print(f"📊 Available worker nodes: {worker_nodes}")
        return worker_nodes
    
    def create_setup_pod(self, pod_name: str, duration: int, target_node: str) -> bool:
        """Create a pod with specific duration on target node"""
        pod_yaml = f"""
apiVersion: v1
kind: Pod
metadata:
  name: {pod_name}
  namespace: {self.namespace}
  annotations:
    scheduling.workload.io/expected-duration-seconds: "{duration}"
spec:
  schedulerName: chronos-kubernetes-scheduler
  nodeSelector:
    kubernetes.io/hostname: {target_node}
  restartPolicy: Never
  containers:
  - name: worker
    image: busybox:latest
    command: ["sh", "-c", "echo 'Setup pod {pod_name} running for {duration}s on {target_node}'; sleep {duration}"]
    resources:
      requests:
        cpu: "100m"
        memory: "64Mi"
"""
        
        # Write pod YAML to temp file
        with open(f'/tmp/{pod_name}.yaml', 'w') as f:
            f.write(pod_yaml)
        
        # Apply pod
        output, code = self.kubectl(['apply', '-f', f'/tmp/{pod_name}.yaml'])
        if code != 0:
            print(f"❌ Failed to create setup pod {pod_name}: {output}")
            return False
            
        print(f"✅ Created setup pod: {pod_name} -> {target_node} ({duration}s)")
        return True
    
    def create_test_pod(self, pod_name: str, duration: int) -> bool:
        """Create the test pod that will be scheduled by Chronos"""
        pod_yaml = f"""
apiVersion: v1
kind: Pod
metadata:
  name: {pod_name}
  namespace: {self.namespace}
  annotations:
    scheduling.workload.io/expected-duration-seconds: "{duration}"
spec:
  schedulerName: chronos-kubernetes-scheduler
  restartPolicy: Never
  containers:
  - name: worker
    image: alpine:latest
    command: ["sh", "-c", "echo 'Test pod {pod_name} running for {duration}s'; sleep {duration}"]
    resources:
      requests:
        cpu: "100m"
        memory: "64Mi"
"""
        
        with open(f'/tmp/{pod_name}.yaml', 'w') as f:
            f.write(pod_yaml)
        
        output, code = self.kubectl(['apply', '-f', f'/tmp/{pod_name}.yaml'])
        if code != 0:
            print(f"❌ Failed to create test pod {pod_name}: {output}")
            return False
            
        print(f"🎯 Created test pod: {pod_name} ({duration}s)")
        return True

    def create_queuesort_test_pod(self, pod_name: str, duration: Optional[int], priority: Optional[int] = None) -> bool:
        """Create a test pod for QueueSort scenarios with optional priority and duration"""
        annotations = ""
        if duration is not None:
            annotations = f"""
  annotations:
    scheduling.workload.io/expected-duration-seconds: "{duration}" """
        
        priority_spec = ""
        if priority is not None:
            priority_spec = f"\n  priority: {priority}"
        
        pod_yaml = f"""
apiVersion: v1
kind: Pod
metadata:
  name: {pod_name}
  namespace: {self.namespace}{annotations}
spec:
  schedulerName: chronos-kubernetes-scheduler{priority_spec}
  restartPolicy: Never
  containers:
  - name: worker
    image: alpine:latest
    command: ["sh", "-c", "echo 'QueueSort test pod {pod_name} running'; sleep {duration or 30}"]
    resources:
      requests:
        cpu: "100m" 
        memory: "64Mi"
"""
        
        with open(f'/tmp/{pod_name}.yaml', 'w') as f:
            f.write(pod_yaml)
        
        output, code = self.kubectl(['apply', '-f', f'/tmp/{pod_name}.yaml'])
        if code != 0:
            print(f"❌ Failed to create QueueSort test pod {pod_name}: {output}")
            return False
            
        duration_str = f"{duration}s" if duration is not None else "no-duration"
        priority_str = f"priority={priority}" if priority is not None else "no-priority"
        print(f"🎯 Created QueueSort test pod: {pod_name} ({duration_str}, {priority_str})")
        return True
    
    def wait_for_pod_scheduled(self, pod_name: str, timeout: int = 120) -> Optional[str]:
        """Wait for pod to be scheduled and return the node name"""
        print(f"⏳ Waiting for pod {pod_name} to be scheduled...")
        
        start_time = time.time()
        while time.time() - start_time < timeout:
            output, code = self.kubectl([
                'get', 'pod', pod_name, '-n', self.namespace,
                '-o', 'jsonpath={.spec.nodeName}'
            ])
            
            if code == 0 and output:
                print(f"✅ Pod {pod_name} scheduled on: {output}")
                return output
                
            polling_interval = self.exec_config.get('polling_interval', 2)
            time.sleep(polling_interval)
        
        print(f"❌ Timeout waiting for pod {pod_name} to be scheduled")
        return None





    def wait_for_multiple_pods_scheduled_via_events(self, pod_names: List[str], timeout: int = 120) -> List[str]:
        """Waits for multiple pods using Scheduled events for precise timing."""
        print(f"⏳ Watching for Scheduled events for pods: {pod_names}")
        scheduled_order = []
        start_time = time.time()

        # Watch for Scheduled events with precise timestamps
        while len(scheduled_order) < len(pod_names) and time.time() - start_time < timeout:
            output, code = self.kubectl([
                'get', 'events', '-n', self.namespace,
                '--field-selector=reason=Scheduled',
                '--sort-by=.firstTimestamp',
                '-o', 'custom-columns=TIME:.firstTimestamp,POD:.involvedObject.name,MESSAGE:.message',
                '--no-headers'
            ])
            
            if code == 0 and output:
                for line in output.strip().split('\n'):
                    if not line:
                        continue
                    parts = line.split(None, 2)  # Split into 3 parts max
                    if len(parts) >= 2:
                        pod_name = parts[1]
                        if pod_name in pod_names and pod_name not in scheduled_order:
                            print(f"✅ Pod {pod_name} was scheduled (via event)")
                            scheduled_order.append(pod_name)
            
            time.sleep(1)  # Check events every second

        if len(scheduled_order) != len(pod_names):
            print(f"❌ Timeout: Only {len(scheduled_order)}/{len(pod_names)} pods were scheduled via events.")
            
            # Fallback to nodeName polling for any missing pods
            print("🔄 Falling back to nodeName polling for remaining pods...")
            remaining_pods = [p for p in pod_names if p not in scheduled_order]
            for pod_name in remaining_pods:
                output, code = self.kubectl([
                    'get', 'pod', pod_name, '-n', self.namespace,
                    '-o', 'jsonpath={.spec.nodeName}'
                ])
                if code == 0 and output:
                    print(f"✅ Pod {pod_name} found scheduled (via polling)")
                    scheduled_order.append(pod_name)
        
        return scheduled_order

    def wait_for_multiple_pods_scheduled(self, pod_names: List[str], timeout: int = 120) -> List[str]:
        """Waits for multiple pods and returns the order they were scheduled in."""
        # Use the more precise event-based method
        return self.wait_for_multiple_pods_scheduled_via_events(pod_names, timeout)

    def cordon_all_nodes(self) -> bool:
        """Cordon all worker nodes to prevent scheduling."""
        worker_nodes = self.get_nodes()
        if not worker_nodes:
            print("❌ No worker nodes found to cordon")
            return False
        
        print(f"🚧 Cordoning {len(worker_nodes)} worker nodes to block scheduling...")
        cordon_timeout = self.exec_config.get('cordon_timeout', 30)
        
        for node in worker_nodes:
            output, code = self.kubectl(['cordon', node])
            if code != 0:
                print(f"❌ Failed to cordon node {node}: {output}")
                # Try to uncordon any nodes we successfully cordoned
                self.uncordon_all_nodes()
                return False
            print(f"✅ Cordoned node: {node}")
        
        print(f"🚧 All {len(worker_nodes)} nodes are now unschedulable")
        return True

    def uncordon_all_nodes(self) -> bool:
        """Uncordon all worker nodes to allow scheduling."""
        worker_nodes = self.get_nodes()
        if not worker_nodes:
            print("❌ No worker nodes found to uncordon")
            return False
        
        print(f"🔓 Uncordoning {len(worker_nodes)} worker nodes to enable scheduling...")
        
        for node in worker_nodes:
            output, code = self.kubectl(['uncordon', node])
            if code != 0:
                print(f"⚠️ Failed to uncordon node {node}: {output}")
                # Continue with other nodes even if one fails
            else:
                print(f"✅ Uncordoned node: {node}")
        
        print(f"🔓 All nodes are now schedulable")
        return True

    def run_queuesort_scenario(self, scenario_name: str, scenario: Dict[str, Any]) -> bool:
        """Runs a QueueSort test scenario using the reliable Node Cordoning method."""
        use_cordon = self.exec_config.get('use_cordon_method', True)
        
        print(f"\n{'='*80}")
        print(f"🎯 Running QueueSort scenario: {scenario_name}")
        print(f"📝 Description: {scenario['description']}")
        
        if use_cordon:
            print(f"🚧 Using Node Cordoning method (100% reliable)")
        else:
            print(f"⚡ Using original rapid creation method")
        print(f"{'='*80}")

        pod_names = [p['name'] for p in scenario['test_pods']]

        if use_cordon:
            return self._run_queuesort_with_cordon(scenario_name, scenario, pod_names)
        else:
            return self._run_queuesort_original_method(scenario_name, scenario, pod_names)

    def _run_queuesort_with_cordon(self, scenario_name: str, scenario: Dict[str, Any], pod_names: List[str]) -> bool:
        """Run QueueSort scenario using the reliable Node Cordoning method."""
        
        # 1. Cordon all worker nodes to prevent any scheduling
        if not self.cordon_all_nodes():
            return False

        try:
            # 2. Create all test pods sequentially (they'll all be Pending due to cordoned nodes)
            print(f"\n📋 Creating {len(pod_names)} test pods (will be queued due to cordoned nodes)...")
            
            for pod_config in scenario['test_pods']:
                duration = pod_config.get('duration')
                priority = pod_config.get('priority')
                
                if not self.create_queuesort_test_pod(pod_config['name'], duration, priority):
                    return False
                
                # Add a small delay to ensure distinct creation timestamps for FIFO ordering
                burst_interval = self.exec_config.get('burst_creation_interval', 0.5)
                time.sleep(burst_interval)

            # 3. Verify all test pods are in Pending state (should be 100% since nodes are cordoned)
            print(f"⏳ Verifying all {len(pod_names)} pods are in Pending state...")
            time.sleep(2)  # Brief wait for pod creation to complete
            
            pending_count = 0
            for pod_name in pod_names:
                output, code = self.kubectl([
                    'get', 'pod', pod_name, '-n', self.namespace,
                    '-o', 'jsonpath={.status.phase}'
                ])
                if code == 0 and output == "Pending":
                    pending_count += 1
            
            print(f"✅ {pending_count}/{len(pod_names)} pods are in Pending state (queued)")
            
            if pending_count < len(pod_names):
                print(f"⚠️ Expected all pods to be Pending with cordoned nodes - this may indicate an issue")

            # 4. Wait for QueueSort plugin to sort the queue
            queue_sort_wait = self.exec_config.get('queue_sort_wait_time', 3)
            print(f"⏳ Waiting {queue_sort_wait}s for QueueSort plugin to sort the queue...")
            time.sleep(queue_sort_wait)

            # 5. Add a small delay before uncordoning to ensure queue is fully processed  
            uncordon_delay = self.exec_config.get('uncordon_delay', 2)
            print(f"⏳ Waiting {uncordon_delay}s before uncordoning to ensure queue processing...")
            time.sleep(uncordon_delay)

            # 6. Uncordon all nodes to trigger scheduling from the sorted queue
            if not self.uncordon_all_nodes():
                print("⚠️ Failed to uncordon nodes, but continuing with test...")

            # 7. Observe the actual scheduling order using precise event monitoring
            timeout = self.exec_config['timeout']
            print(f"👀 Monitoring scheduling order for {timeout}s...")
            actual_order = self.wait_for_multiple_pods_scheduled(pod_names, timeout)
            
            return self._analyze_queuesort_results(scenario, pod_names, actual_order, "Node Cordoning Method")

        finally:
            # Always try to uncordon nodes in case of any errors
            print("🔄 Ensuring all nodes are uncordoned...")
            self.uncordon_all_nodes()

    def _run_queuesort_original_method(self, scenario_name: str, scenario: Dict[str, Any], pod_names: List[str]) -> bool:
        """Run QueueSort scenario using the original rapid creation method."""
        
        print("\n📋 Creating test pods in rapid sequence...")
        
        for pod_config in scenario['test_pods']:
            duration = pod_config.get('duration')
            priority = pod_config.get('priority')
            
            if not self.create_queuesort_test_pod(pod_config['name'], duration, priority):
                return False
            
            # Add a small delay to ensure distinct creation timestamps for FIFO ordering
            burst_interval = self.exec_config.get('burst_creation_interval', 0.5)
            time.sleep(burst_interval)

        # Wait for QueueSort processing and scheduling
        queue_sort_wait = self.exec_config.get('queue_sort_wait_time', 3)
        print(f"⏳ Waiting {queue_sort_wait}s for QueueSort plugin to process queue...")
        time.sleep(queue_sort_wait)

        # Observe the actual scheduling order using precise event monitoring
        timeout = self.exec_config['timeout']
        print(f"👀 Monitoring scheduling order for {timeout}s...")
        actual_order = self.wait_for_multiple_pods_scheduled(pod_names, timeout)
        
        return self._analyze_queuesort_results(scenario, pod_names, actual_order, "Original Method")

    def _analyze_queuesort_results(self, scenario: Dict[str, Any], pod_names: List[str], actual_order: List[str], method: str) -> bool:
        """Analyze and report QueueSort test results."""
        expected_order = scenario['expected_scheduling_order']
        success = (actual_order == expected_order)
        status = "✅ PASSED" if success else "❌ FAILED"

        print(f"\n📊 QUEUESORT RESULTS ({method}):")
        print(f"Creation Order:              {[p['name'] for p in scenario['test_pods']]}")
        print(f"Expected Scheduling Order:   {expected_order}")
        print(f"Actual Scheduling Order:     {actual_order}")
        
        # Show pod details for debugging
        print("\n🔍 POD DETAILS:")
        for pod_config in scenario['test_pods']:
            name = pod_config['name']
            duration = pod_config.get('duration', "None")
            priority = pod_config.get('priority', "None")
            print(f"  {name}: duration={duration}s, priority={priority}")

        # Get scheduler logs showing queue sort activity
        print("\n📋 QUEUESORT LOGS:")
        self.get_queuesort_scheduler_logs()
        
        # Show final pod states for debugging
        print("\n📊 FINAL POD STATES:")
        for pod_name in pod_names:
            output, code = self.kubectl([
                'get', 'pod', pod_name, '-n', self.namespace,
                '-o', 'custom-columns=NAME:.metadata.name,STATUS:.status.phase,NODE:.spec.nodeName,START:.status.startTime',
                '--no-headers'
            ])
            if code == 0:
                print(f"  {output}")
        
        print(f"\n{status}")
        
        if not success:
            print("\n🔍 DEBUGGING INFO:")
            print(f"This test validates the QueueSort plugin's Less() function using {method}.")
            print("Expected behavior:")
            print("1. Pods with higher priority should be scheduled first")
            print("2. Among pods with same priority, longer duration should be scheduled first")
            print("3. Pods without duration annotation should be scheduled last")
            print("4. FIFO ordering should break ties")
            
            if "Cordoning" in method:
                print(f"\n🚧 Node Cordoning method ensures:")
                print(f"- Eliminates race conditions completely")
                print(f"- All pods guaranteed to be Pending simultaneously")  
                print(f"- No resource calculations needed")
                print(f"- Uses Kubernetes built-in scheduling control")
                print(f"- Most reliable method for QueueSort testing")
            else:
                print(f"\n⚡ Original method limitations:")
                print(f"- May have race conditions if pods are scheduled immediately")
                print(f"- Less reliable in fast scheduling environments")
                print(f"- Consider enabling 'use_cordon_method: true' for most reliable testing")
        
        return success

    def get_queuesort_scheduler_logs(self) -> None:
        """Get and display scheduler logs relevant to QueueSort functionality"""
        # Find scheduler pod
        output, code = self.kubectl([
            'get', 'pods', '-n', 'chronos-system',
            '-l', 'app.kubernetes.io/name=chronos-kubernetes-scheduler',
            '-o', 'jsonpath={.items[0].metadata.name}'
        ])
        
        if code != 0 or not output:
            print("❌ Could not find scheduler pod")
            return
        
        scheduler_pod = output
        
        # Get logs containing QueueSort activity
        output, code = self.kubectl([
            'logs', '-n', 'chronos-system', scheduler_pod, '--tail=200'
        ])
        
        if code != 0:
            print(f"❌ Failed to get scheduler logs: {output}")
            return
        
        # Filter logs for QueueSort activity
        queue_logs = []
        for line in output.split('\n'):
            if any(keyword in line for keyword in ['Less(', 'QueueSort', 'priority', 'duration', 'CHRONOS_SCORE']):
                queue_logs.append(line)
        
        if queue_logs:
            print("Found QueueSort/scheduling activity:")
            for line in queue_logs[-20:]:  # Show last 20 relevant lines
                print(f"  {line}")
        else:
            print("No QueueSort activity found in logs (this may indicate the plugin is disabled)")
    
    def get_scheduler_logs(self, pod_name: str) -> str:
        """Get scheduler logs for the test pod"""
        # Find scheduler pod
        output, code = self.kubectl([
            'get', 'pods', '-n', 'chronos-system',
            '-l', 'app.kubernetes.io/name=chronos-kubernetes-scheduler',
            '-o', 'jsonpath={.items[0].metadata.name}'
        ])
        
        if code != 0 or not output:
            print("❌ Could not find scheduler pod")
            return ""
        
        scheduler_pod = output
        print(f"📋 Getting logs from scheduler pod: {scheduler_pod}")
        
        # Get logs containing our test pod
        output, code = self.kubectl([
            'logs', '-n', 'chronos-system', scheduler_pod
        ])
        
        if code != 0:
            print(f"❌ Failed to get scheduler logs: {output}")
            return ""
        
        # Filter logs for our test pod
        relevant_logs = []
        for line in output.split('\n'):
            if pod_name in line and ('CHRONOS_SCORE' in line or 'Successfully bound' in line):
                relevant_logs.append(line)
        
        return '\n'.join(relevant_logs)
    
    def analyze_scheduling_decision(self, logs: str, expected_node: str, expected_strategy: str) -> Dict[str, Any]:
        """Analyze scheduler logs and return decision details"""
        result = {
            'chronos_scores': [],
            'chosen_node': None,
            'strategy_used': None,
            'success': False
        }
        
        # Parse CHRONOS_SCORE lines
        chronos_pattern = r'CHRONOS_SCORE: Pod=([^,]+), Node=([^,]+), Strategy=([^,]+), NewPodDuration=(\d+)s, maxRemainingTime=(\d+)s, ExtensionDuration=(\d+)s, CompletionTime=([^,]+), FinalScore=(-?\d+)'
        
        for line in logs.split('\n'):
            match = re.search(chronos_pattern, line)
            if match:
                result['chronos_scores'].append({
                    'pod': match.group(1),
                    'node': match.group(2), 
                    'strategy': match.group(3),
                    'new_pod_duration': int(match.group(4)),
                    'max_remaining_time': int(match.group(5)),
                    'extension_duration': int(match.group(6)),
                    'completion_time': match.group(7),
                    'final_score': int(match.group(8))
                })
        
        # Parse successful binding
        binding_pattern = r'Successfully bound pod to node.*pod="([^"]+)".*node="([^"]+)"'
        for line in logs.split('\n'):
            match = re.search(binding_pattern, line)
            if match:
                result['chosen_node'] = match.group(2)
        
        # Determine if expectations were met
        if expected_node == "any":
            result['success'] = result['chosen_node'] is not None
        else:
            result['success'] = result['chosen_node'] == expected_node
            
        return result
    
    def run_scenario(self, scenario_name: str, scenario: Dict[str, Any]) -> bool:
        """Run a single test scenario"""
        print(f"\n{'='*80}")
        print(f"🎯 Running scenario: {scenario_name}")
        print(f"📝 Description: {scenario['description']}")
        print(f"{'='*80}")
        
        # Setup initial conditions
        print("\n📋 Setting up initial conditions...")
        for node, pods in scenario['setup_pods'].items():
            for pod_config in pods:
                if not self.create_setup_pod(
                    pod_config['name'],
                    pod_config['duration'], 
                    node
                ):
                    return False
        
        # Wait for setup pods to be running
        setup_wait = self.exec_config['setup_wait_time']
        print(f"⏳ Waiting {setup_wait}s for setup pods to be running...")
        time.sleep(setup_wait)
        
        # Create test pod
        new_pod = scenario['new_pod']
        test_wait = self.exec_config['test_wait_time']
        print(f"⏳ Waiting {test_wait}s before scheduling test pod...")
        time.sleep(test_wait)
        
        if not self.create_test_pod(new_pod['name'], new_pod['duration']):
            return False
        
        # Wait for scheduling
        timeout = self.exec_config['timeout']
        actual_node = self.wait_for_pod_scheduled(new_pod['name'], timeout)
        if not actual_node:
            return False
        
        # Analyze results
        logs = self.get_scheduler_logs(new_pod['name'])
        analysis = self.analyze_scheduling_decision(
            logs, 
            new_pod['expected_node'],
            new_pod['expected_strategy']
        )
        
        # Print results
        print("\n📊 RESULTS:")
        print(f"Expected node: {new_pod['expected_node']}")
        print(f"Actual node: {actual_node}")
        print(f"Expected strategy: {new_pod['expected_strategy']}")
        
        if analysis['chronos_scores']:
            print("\n🔍 CHRONOS_SCORE details:")
            for score in analysis['chronos_scores']:
                print(f"  Node: {score['node']}")
                print(f"  Strategy: {score['strategy']} ")
                print(f"  Score: {score['final_score']}")
                print(f"  Max remaining: {score['max_remaining_time']}s")
                print(f"  Extension needed: {score['extension_duration']}s")
                print()
        
        success = analysis['success']
        status = "✅ PASSED" if success else "❌ FAILED"
        print(f"\n{status}")
        
        return success
    
    def run_all_scenarios(self) -> bool:
        """Run all configured scenarios"""
        start_time = time.time()
        print("🚀 Starting Chronos Scheduler Simulation Tests")
        
        # Get overall execution timeout from config (in minutes, convert to seconds)
        overall_timeout = self.exec_config.get('overall_timeout_minutes', 10) * 60
        print(f"⏱️  Overall execution timeout: {overall_timeout // 60} minutes")
        
        if not self.create_namespace():
            return False
        
        scenarios = self.config['scenarios']
        passed = 0
        total = len(scenarios)
        
        try:
            for scenario_name, scenario in scenarios.items():
                # Check overall timeout
                elapsed = time.time() - start_time
                if elapsed >= overall_timeout:
                    print(f"\n⚠️ Overall execution timeout reached ({elapsed:.1f}s >= {overall_timeout}s)")
                    print(f"Completed {passed}/{total} scenarios before timeout")
                    break
                
                # Check if this is a QueueSort scenario
                if 'test_pods' in scenario:
                    print(f"\n🔍 Detected QueueSort scenario: {scenario_name}")
                    if self.run_queuesort_scenario(scenario_name, scenario):
                        passed += 1
                else: # It's a standard Score scenario
                    print(f"\n🔍 Detected standard Score scenario: {scenario_name}")
                    if self.run_scenario(scenario_name, scenario):
                        passed += 1
                
                # Cleanup between scenarios
                print(f"🧹 Cleaning up pods from scenario: {scenario_name}")
                self.kubectl(['delete', 'pods', '--all', '-n', self.namespace])
                cleanup_wait = self.exec_config.get('cleanup_wait', 5)
                time.sleep(cleanup_wait)
                
        finally:
            self.cleanup_namespace()
        
        print(f"\n{'='*80}")
        elapsed_time = time.time() - start_time
        print(f"🏁 SIMULATION RESULTS: {passed}/{total} scenarios passed")
        print(f"⏱️  Total execution time: {elapsed_time:.1f}s")
        print(f"{'='*80}")
        
        return passed == total

if __name__ == "__main__":
    import argparse
    
    parser = argparse.ArgumentParser(description='Run Chronos scheduler simulations')
    parser.add_argument('--config', default='simulations.yaml', help='Simulation config file')
    parser.add_argument('--kubeconfig', help='Path to kubeconfig file')  
    parser.add_argument('--scenario', help='Run specific scenario only')
    
    args = parser.parse_args()
    
    simulator = ChronosSimulator(args.config, args.kubeconfig)
    
    if args.scenario:
        # Run single scenario
        scenario = simulator.config['scenarios'].get(args.scenario)
        if not scenario:
            print(f"❌ Scenario '{args.scenario}' not found")
            sys.exit(1)
            
        simulator.create_namespace()
        
        # Check if this is a QueueSort scenario
        if 'test_pods' in scenario:
            print(f"🔍 Running single QueueSort scenario: {args.scenario}")
            success = simulator.run_queuesort_scenario(args.scenario, scenario)
        else:
            print(f"🔍 Running single Score scenario: {args.scenario}")
            success = simulator.run_scenario(args.scenario, scenario)
            
        simulator.cleanup_namespace()
        sys.exit(0 if success else 1)
    else:
        # Run all scenarios
        success = simulator.run_all_scenarios()
        sys.exit(0 if success else 1)
