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
        
        # Check if this is a batch scenario or individual scenario
        if 'batch_pods' in scenario:
            # Handle batch scenario
            return self.run_batch_scenario(scenario)
        
        # Handle individual scenario
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
    
    def run_batch_scenario(self, scenario):
        """Handle batch scheduling scenarios with multiple pods"""
        print(f"📦 Running BATCH scenario")
        
        batch_pods = scenario['batch_pods']
        batch_wait = self.exec_config.get('batch_wait_time', 8)
        batch_timeout = self.exec_config.get('batch_timeout', 90)
        
        print(f"⏳ Waiting {batch_wait}s before creating batch pods...")
        time.sleep(batch_wait)
        
        # Create all batch pods at roughly the same time
        created_pods = []
        for pod_config in batch_pods:
            if self.create_test_pod(pod_config['name'], pod_config['duration']):
                created_pods.append(pod_config)
                time.sleep(0.5)  # Small delay between pods
            else:
                print(f"❌ Failed to create pod: {pod_config['name']}")
                return False
        
        print(f"📦 Created {len(created_pods)} batch pods, waiting for scheduling...")
        
        # Wait for all pods to be scheduled
        scheduled_pods = {}
        for pod_config in created_pods:
            actual_node = self.wait_for_pod_scheduled(pod_config['name'], batch_timeout)
            if actual_node:
                scheduled_pods[pod_config['name']] = actual_node
                print(f"✅ Pod '{pod_config['name']}' scheduled to: {actual_node}")
            else:
                print(f"❌ Pod '{pod_config['name']}' failed to schedule within {batch_timeout}s")
                return False
        
        # Validate expectations for each pod
        all_correct = True
        print(f"\n📊 BATCH RESULTS:")
        for pod_config in batch_pods:
            pod_name = pod_config['name']
            expected_node = pod_config.get('expected_node')
            actual_node = scheduled_pods.get(pod_name)
            
            if expected_node and actual_node:
                if actual_node == expected_node:
                    print(f"✅ {pod_name}: Expected {expected_node}, got {actual_node}")
                else:
                    print(f"❌ {pod_name}: Expected {expected_node}, got {actual_node}")
                    all_correct = False
            else:
                print(f"ℹ️  {pod_name}: No expected_node specified, got {actual_node}")
        
        return all_correct
    
    def run_all_scenarios(self) -> bool:
        """Run all configured scenarios"""
        start_time = time.time()
        print("🚀 Starting Chronos Scheduler Simulation Tests")
        
        if not self.create_namespace():
            return False
        
        scenarios = self.config['scenarios']
        passed = 0
        total = len(scenarios)
        
        try:
            for scenario_name, scenario in scenarios.items():
                if self.run_scenario(scenario_name, scenario):
                    passed += 1
                
                # Cleanup between scenarios
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
        success = simulator.run_scenario(args.scenario, scenario)
        simulator.cleanup_namespace()
        sys.exit(0 if success else 1)
    else:
        # Run all scenarios
        success = simulator.run_all_scenarios()
        sys.exit(0 if success else 1)
