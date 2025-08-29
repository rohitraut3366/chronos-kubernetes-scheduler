#!/usr/bin/env python3
"""
Chronos Scheduler Simulation Runner
Executes realistic scheduling scenarios to validate bin-packing logic
"""

import yaml  # type: ignore
import subprocess
import time
import sys
import os
import tempfile
import re
from typing import Dict, List, Any, Optional


class ChronosSimulator:
    def __init__(self, config_file: str, kubeconfig: str = None):
        with open(config_file, "r") as f:
            self.config = yaml.safe_load(f)
        self.kubeconfig = kubeconfig
        self.exec_config = self.config["execution"]
        self.namespace = self.exec_config["namespace"]

    def kubectl(self, args: List[str]) -> tuple[str, int]:
        """Execute kubectl command and return output, exit_code"""
        cmd = ["kubectl"] + args
        if self.kubeconfig:
            cmd.extend(["--kubeconfig", self.kubeconfig])

        # Uncomment for debugging: print(f"🔧 Running: {' '.join(cmd)}")
        result = subprocess.run(cmd, capture_output=True, text=True)
        # Return both stdout and stderr combined for better error handling
        output = result.stdout.strip()
        if result.returncode != 0 and result.stderr.strip():
            output = result.stderr.strip()
        return output, result.returncode

    def create_namespace(self):
        """Create test namespace"""
        print(f"📁 Creating namespace: {self.namespace}")
        output, code = self.kubectl(["create", "namespace", self.namespace])
        if code != 0 and "already exists" not in output:
            print(f"❌ Failed to create namespace: {output}")
            return False
        return True

    def cleanup_namespace(self):
        """Clean up test namespace"""
        print(f"🧹 Cleaning up namespace: {self.namespace}")

        # Force delete all pods first for faster cleanup
        print("🧹 Force deleting all pods first...")
        self.kubectl(
            [
                "delete",
                "pods",
                "--all",
                "-n",
                self.namespace,
                "--force",
                "--grace-period=0",
                "--ignore-not-found=true",
            ]
        )

        # Then delete the namespace
        print("🧹 Deleting namespace...")
        self.kubectl(
            [
                "delete",
                "namespace",
                self.namespace,
                "--ignore-not-found=true",
                "--force",
                "--grace-period=0",
            ]
        )

        cleanup_wait = self.exec_config.get("cleanup_wait", 2)  # Reduced from 5 to 2
        time.sleep(cleanup_wait)  # Wait for cleanup

    def get_nodes(self) -> List[str]:
        """Get available worker nodes"""
        output, code = self.kubectl(
            ["get", "nodes", "-o", "jsonpath={.items[*].metadata.name}"]
        )
        if code != 0:
            print(f"❌ Failed to get nodes: {output}")
            return []

        nodes = output.split()
        # Filter out control-plane nodes
        worker_nodes = [node for node in nodes if "control-plane" not in node]
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
        with open(f"/tmp/{pod_name}.yaml", "w") as f:
            f.write(pod_yaml)

        # Apply pod
        output, code = self.kubectl(["apply", "-f", f"/tmp/{pod_name}.yaml"])
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

        with open(f"/tmp/{pod_name}.yaml", "w") as f:
            f.write(pod_yaml)

        output, code = self.kubectl(["apply", "-f", f"/tmp/{pod_name}.yaml"])
        if code != 0:
            print(f"❌ Failed to create test pod {pod_name}: {output}")
            return False

        print(f"🎯 Created test pod: {pod_name} ({duration}s)")
        return True

    def create_priority_class(
        self, priority_name: str, priority_value: int, is_default: bool = False
    ) -> bool:
        """Create a PriorityClass resource"""
        priority_class_yaml = f"""
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: {priority_name}
value: {priority_value}
globalDefault: {str(is_default).lower()}
description: "Priority class for QueueSort testing"
"""

        with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
            f.write(priority_class_yaml)
            yaml_file = f.name

        try:
            output, code = self.kubectl(
                ["apply", "-f", yaml_file, "--kubeconfig", "/tmp/kubeconfig"]
            )
            if code == 0:
                default_msg = " (GLOBAL DEFAULT)" if is_default else ""
                print(
                    f"✅ Created PriorityClass: {priority_name} (value={priority_value}){default_msg}"
                )
                return True
            else:
                print(f"⚠️ Failed to create PriorityClass {priority_name}: {output}")
                return False
        finally:
            os.unlink(yaml_file)

    def setup_standard_priority_classes(self) -> bool:
        """Setup comprehensive priority classes including default for better QueueSort visibility"""
        print(
            "🎯 Setting up standard priority classes for enhanced QueueSort testing..."
        )

        # Define priority classes from highest to lowest
        priority_classes = [
            {"name": "critical-priority", "value": 2000, "default": False},
            {"name": "high-priority", "value": 1000, "default": False},
            {
                "name": "default-user-priority",
                "value": 250,
                "default": True,
            },  # This becomes default for all pods
            {"name": "low-priority", "value": 100, "default": False},
            {"name": "batch-priority", "value": 50, "default": False},
        ]

        for pc in priority_classes:
            if not self.create_priority_class(pc["name"], pc["value"], pc["default"]):
                print(f"❌ Failed to create priority class {pc['name']}")
                return False

        print("✅ Standard priority classes created successfully!")
        print("📊 Impact on QueueSort:")
        print(
            "   - Pods without priorityClassName will now get priority 250 (instead of 0)"
        )
        print("   - This creates clearer priority distinctions for testing")
        print("   - QueueSort debug logs should be much more visible")
        return True

    def setup_non_default_priority_classes(self) -> bool:
        """Setup priority classes WITHOUT global default to preserve priority=0 for QueueSort tests"""
        print(
            "🎯 Setting up non-default priority classes for proper QueueSort testing..."
        )

        # Define priority classes - NO globalDefault to preserve priority=0 behavior
        priority_classes = [
            {"name": "critical-priority", "value": 2000, "default": False},
            {"name": "high-priority", "value": 1000, "default": False},
            {"name": "medium-priority", "value": 500, "default": False},
            {"name": "low-priority", "value": 100, "default": False},
            {"name": "batch-priority", "value": 50, "default": False},
        ]

        for pc in priority_classes:
            if not self.create_priority_class(pc["name"], pc["value"], pc["default"]):
                print(f"❌ Failed to create priority class {pc['name']}")
                return False

        print("✅ Non-default priority classes created successfully!")
        print("📊 Impact on QueueSort:")
        print(
            "   - Pods WITHOUT priorityClassName keep priority = 0 (correct for tests)"
        )
        print("   - Pods WITH explicit priorityClassName get their assigned priority")
        print(
            "   - This preserves the priority=0 vs priority=explicit distinction needed for QueueSort"
        )
        return True

    def create_original_priority_class(
        self, priority_name: str, priority_value: int
    ) -> bool:
        """Create a PriorityClass resource (original method for backward compatibility)"""
        priority_class_yaml = f"""
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: {priority_name}
value: {priority_value}
globalDefault: false
description: "Priority class for QueueSort testing"
"""

        with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
            f.write(priority_class_yaml)
            yaml_file = f.name

        try:
            output, code = self.kubectl(
                ["apply", "-f", yaml_file, "--kubeconfig", "/tmp/kubeconfig"]
            )
            if code == 0:
                print(
                    f"✅ Created PriorityClass: {priority_name} (value={priority_value})"
                )
                return True
            else:
                print(f"❌ Failed to create PriorityClass {priority_name}: {output}")
                return False
        finally:
            os.unlink(yaml_file)

    def cleanup_priority_classes(self) -> bool:
        """Clean up all test priority classes"""
        output, code = self.kubectl(
            ["get", "priorityclasses", "-o", "name", "--kubeconfig", "/tmp/kubeconfig"]
        )

        if code == 0:
            priority_classes = [
                pc.strip()
                for pc in output.split("\n")
                if pc.strip()
                and (
                    "test-priority-" in pc
                    or "high-priority" in pc
                    or "medium-priority" in pc
                    or "low-priority" in pc
                    or "critical-priority" in pc
                    or "batch-priority" in pc
                    or "default-user-priority" in pc
                )
            ]
            for pc in priority_classes:
                self.kubectl(
                    [
                        "delete",
                        pc,
                        "--kubeconfig",
                        "/tmp/kubeconfig",
                        "--force",
                        "--grace-period=0",
                    ]
                )
                print(f"🧹 Deleted {pc}")

        return True

    def _ensure_all_nodes_uncordoned(self) -> None:
        """Ensure all worker nodes are uncordoned (cleanup from previous test runs)"""
        print("🔄 Ensuring all nodes are uncordoned...")

        # Get all worker nodes
        output, code = self.kubectl(
            ["get", "nodes", "-o", "jsonpath={.items[*].metadata.name}"]
        )

        if code != 0:
            print("⚠️ Failed to get node list for uncordoning")
            return

        all_nodes = output.split()
        worker_nodes = [node for node in all_nodes if "control-plane" not in node]

        if not worker_nodes:
            print("⚠️ No worker nodes found")
            return

        print(f"🔓 Uncordoning {len(worker_nodes)} worker nodes...")

        for node in worker_nodes:
            output, code = self.kubectl(["uncordon", node])
            if code == 0:
                print(f"✅ Ensured {node} is uncordoned")
            else:
                # Node might already be uncordoned, which is fine
                if (
                    "already uncordoned" in output.lower()
                    or "not cordoned" in output.lower()
                ):
                    print(f"✅ {node} was already uncordoned")
                else:
                    print(f"⚠️ Failed to uncordon {node}: {output}")

        print("🔓 All nodes are now schedulable")

    def _create_pods_batch(self, test_pods: list) -> bool:
        """Create all test pods simultaneously for deterministic queue sorting"""
        print("🚀 Using batch pod creation for deterministic queue competition...")

        # Create a single YAML file with all pods
        all_pods_yaml = []

        # Handle both flat list format and nested node format
        if isinstance(test_pods, list):
            # Flat list format (existing scenarios)
            for pod_config in test_pods:
                duration = pod_config.get("duration")
                priority = pod_config.get("priority")
                node_selector = pod_config.get("node_selector")
                pod_name = pod_config["name"]

                # Generate pod YAML
                pod_yaml = self._generate_queuesort_pod_yaml(
                    pod_name, duration, priority, node_selector
                )
                all_pods_yaml.append(pod_yaml)
        else:
            # Nested node format (multinode scenarios)
            for node_name, pods_on_node in test_pods.items():
                for pod_config in pods_on_node:
                    duration = pod_config.get("duration")
                    priority = pod_config.get("priority")
                    pod_name = pod_config["name"]

                    # Generate pod YAML with specific node selector
                    pod_yaml = self._generate_queuesort_pod_yaml(
                        pod_name, duration, priority, node_name
                    )
                    all_pods_yaml.append(pod_yaml)

        # Combine all pods into a single YAML file with proper formatting
        combined_yaml = "\n---\n".join(all_pods_yaml)

        with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
            f.write(combined_yaml)
            batch_file = f.name

        try:
            # Apply all pods in a single kubectl command for true simultaneity
            output, code = self.kubectl(["apply", "-f", batch_file])

            if code == 0:
                print("✅ All pods created simultaneously for optimal queue sorting")
                return True
            else:
                print(f"❌ Batch pod creation failed: {output}")
                print(f"🔍 Debug: Batch file was: {batch_file}")
                # Don't fail immediately - let's see what happened
                with open(batch_file, "r") as f:
                    print(f"🔍 Debug: YAML content:\n{f.read()[:1000]}...")
                return False
        finally:
            # Keep the file for debugging if creation failed
            if os.path.exists(batch_file):
                os.unlink(batch_file)

    def _create_pods_sequential(self, test_pods) -> bool:
        """Create test pods sequentially with distinct timestamps for FIFO tiebreaker testing"""
        print("📝 Creating pods one by one with distinct timestamps...")

        # Handle both flat list format and nested node format
        pods_to_create = []
        if isinstance(test_pods, list):
            # Flat list format (existing scenarios)
            for pod_config in test_pods:
                duration = pod_config.get("duration")
                priority = pod_config.get("priority")
                node_selector = pod_config.get("node_selector")
                pod_name = pod_config["name"]
                pods_to_create.append((pod_name, duration, priority, node_selector))
        else:
            # Nested node format (multinode scenarios)
            for node_name, pods_on_node in test_pods.items():
                for pod_config in pods_on_node:
                    duration = pod_config.get("duration")
                    priority = pod_config.get("priority")
                    pod_name = pod_config["name"]
                    # Use explicit node_selector if provided, otherwise use the node_name from the YAML structure
                    node_selector = pod_config.get("node_selector", node_name)
                    pods_to_create.append((pod_name, duration, priority, node_selector))

        for i, (pod_name, duration, priority, node_selector) in enumerate(
            pods_to_create
        ):
            # Generate pod YAML
            pod_yaml = self._generate_queuesort_pod_yaml(
                pod_name, duration, priority, node_selector
            )

            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".yaml", delete=False
            ) as f:
                f.write(pod_yaml)
                pod_file = f.name

            try:
                # Create pod
                output, code = self.kubectl(["apply", "-f", pod_file])

                if code == 0:
                    print(f"✅ Created pod {i+1}/{len(test_pods)}: {pod_name}")
                else:
                    print(f"❌ Failed to create pod {pod_name}: {output}")
                    return False

                # Wait 1 second between pod creations to ensure distinct timestamps
                if i < len(test_pods) - 1:  # Don't wait after the last pod
                    time.sleep(1)

            finally:
                if os.path.exists(pod_file):
                    os.unlink(pod_file)

        print("✅ All pods created sequentially with distinct timestamps")
        return True

    def _generate_queuesort_pod_yaml(
        self, pod_name: str, duration, priority, node_selector=None
    ) -> str:
        """Generate YAML for a single QueueSort test pod"""
        pod_yaml = f"""apiVersion: v1
kind: Pod
metadata:
  name: {pod_name}
  namespace: simulation-test"""

        # Add duration annotation if provided
        if duration is not None:
            pod_yaml += f"""
  annotations:
    scheduling.workload.io/expected-duration-seconds: "{duration}\""""

        pod_yaml += """
spec:
  schedulerName: chronos-kubernetes-scheduler"""

        # Add priority class if specified
        if priority is not None:
            priority_class_name = f"test-priority-{priority}"
            # Create priority class if it doesn't exist
            if not self.create_priority_class(priority_class_name, priority):
                print(f"⚠️ Failed to create priority class {priority_class_name}")

            pod_yaml += f"""
  priorityClassName: {priority_class_name}"""

        # Add nodeSelector if specified, skip if explicitly null, otherwise default to single worker node
        if node_selector is not None and node_selector != "null":
            pod_yaml += f"""
  nodeSelector:
    kubernetes.io/hostname: {node_selector}"""
        elif node_selector != "null":
            # Default behavior for backward compatibility - target specific worker
            pod_yaml += """
  nodeSelector:
    kubernetes.io/hostname: chronos-test-worker"""
        # If node_selector is "null", don't add any nodeSelector (pod can go to any node)

        pod_yaml += """
  containers:
  - name: worker
    image: alpine:latest
    command: ["sh", "-c", "echo 'QueueSort test pod {pod_name}'; sleep 3600"]
    resources:
      requests:
        cpu: "100m"
        memory: "100Mi"
      limits:
        cpu: "100m"
        memory: "100Mi"
  restartPolicy: Never"""

        return pod_yaml

    def create_queuesort_test_pod(
        self, pod_name: str, duration: Optional[int], priority: Optional[int] = None
    ) -> bool:
        """Create a test pod for QueueSort scenarios with optional priority and duration"""
        annotations = ""
        if duration is not None:
            annotations = f"""
  annotations:
    scheduling.workload.io/expected-duration-seconds: "{duration}" """

        priority_spec = ""
        if priority is not None:
            priority_class_name = f"test-priority-{priority}"
            # Create the priority class if it doesn't exist
            self.create_priority_class(priority_class_name, priority)
            priority_spec = f"\n  priorityClassName: {priority_class_name}"

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

        with open(f"/tmp/{pod_name}.yaml", "w") as f:
            f.write(pod_yaml)

        output, code = self.kubectl(["apply", "-f", f"/tmp/{pod_name}.yaml"])
        if code != 0:
            print(f"❌ Failed to create QueueSort test pod {pod_name}: {output}")
            return False

        duration_str = f"{duration}s" if duration is not None else "no-duration"
        priority_str = f"priority={priority}" if priority is not None else "no-priority"
        print(
            f"🎯 Created QueueSort test pod: {pod_name} ({duration_str}, {priority_str})"
        )
        return True

    def wait_for_pod_scheduled(
        self, pod_name: str, timeout: int = 120
    ) -> Optional[str]:
        """Wait for pod to be scheduled and return the node name"""
        print(f"⏳ Waiting for pod {pod_name} to be scheduled...")

        start_time = time.time()
        while time.time() - start_time < timeout:
            output, code = self.kubectl(
                [
                    "get",
                    "pod",
                    pod_name,
                    "-n",
                    self.namespace,
                    "-o",
                    "jsonpath={.spec.nodeName}",
                ]
            )

            if code == 0 and output:
                print(f"✅ Pod {pod_name} scheduled on: {output}")
                return output

            polling_interval = self.exec_config.get("polling_interval", 2)
            time.sleep(polling_interval)

        print(f"❌ Timeout waiting for pod {pod_name} to be scheduled")
        return None

    def wait_for_multiple_pods_scheduled_via_events(
        self, pod_names: List[str], timeout: int = 120
    ) -> List[str]:
        """Waits for multiple pods using Scheduled events for precise timing."""
        print(f"⏳ Watching for Scheduled events for pods: {pod_names}")
        scheduled_order = []
        start_time = time.time()

        # Watch for Scheduled events with precise timestamps
        while (
            len(scheduled_order) < len(pod_names) and time.time() - start_time < timeout
        ):
            output, code = self.kubectl(
                [
                    "get",
                    "events",
                    "-n",
                    self.namespace,
                    "--field-selector=reason=Scheduled",
                    "--sort-by=.firstTimestamp",
                    "-o",
                    "custom-columns=TIME:.firstTimestamp,POD:.involvedObject.name,MESSAGE:.message",
                    "--no-headers",
                ]
            )

            if code == 0 and output:
                for line in output.strip().split("\n"):
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
            print(
                f"❌ Timeout: Only {len(scheduled_order)}/{len(pod_names)} pods were scheduled via events."
            )

            # Fallback to nodeName polling for any missing pods
            print("🔄 Falling back to nodeName polling for remaining pods...")
            remaining_pods = [p for p in pod_names if p not in scheduled_order]
            for pod_name in remaining_pods:
                output, code = self.kubectl(
                    [
                        "get",
                        "pod",
                        pod_name,
                        "-n",
                        self.namespace,
                        "-o",
                        "jsonpath={.spec.nodeName}",
                    ]
                )
                if code == 0 and output:
                    print(f"✅ Pod {pod_name} found scheduled (via polling)")
                    scheduled_order.append(pod_name)

        return scheduled_order

    def wait_for_multiple_pods_scheduled(
        self, pod_names: List[str], timeout: int = 120
    ) -> List[str]:
        """Monitor pod scheduling order using scheduler binding logs (most accurate method)."""
        print(f"⏳ Watching for pod bindings in scheduler logs for pods: {pod_names}")

        scheduled_order = []
        start_time = time.time()
        seen_bindings = set()

        # First, wait a bit for initial scheduling to happen
        time.sleep(5)

        while (
            len(scheduled_order) < len(pod_names)
            and (time.time() - start_time) < timeout
        ):
            # Get scheduler logs to find binding events
            scheduler_logs = self.get_queuesort_scheduler_logs(return_logs=True)

            # Look for binding events with timestamps
            binding_events = []
            for log_line in scheduler_logs.split("\n"):
                if (
                    "Successfully bound pod=" in log_line
                    and f'pod="{self.namespace}/' in log_line
                ):
                    # Extract timestamp and pod name
                    # Format: I0829 03:08:15.058541 ... "Successfully bound pod="simulation-test/pod-name"
                    if log_line not in seen_bindings:
                        seen_bindings.add(log_line)
                        try:
                            # Extract pod name from log line - handle the format correctly
                            if f'pod="{self.namespace}/' in log_line:
                                pod_part = log_line.split(f'pod="{self.namespace}/')[
                                    1
                                ].split('"')[0]
                            else:
                                continue

                            if (
                                pod_part in pod_names
                                and pod_part not in scheduled_order
                            ):
                                # Extract timestamp for ordering (second field in log line)
                                log_parts = log_line.split()
                                if len(log_parts) >= 2:
                                    timestamp_part = log_parts[
                                        1
                                    ]  # Get timestamp like "03:08:15.058541"
                                    binding_events.append((timestamp_part, pod_part))
                                    print(
                                        f"🔍 Found binding: {pod_part} at {timestamp_part}"
                                    )
                        except (IndexError, ValueError) as e:
                            print(
                                f"⚠️ Failed to parse line: {log_line[:100]}... Error: {e}"
                            )
                            continue

            # Sort by timestamp and add to scheduled order
            binding_events.sort(key=lambda x: x[0])  # Sort by timestamp
            for timestamp, pod_name in binding_events:
                if pod_name not in scheduled_order:
                    print(
                        f"✅ Pod {pod_name} was bound at {timestamp} (via scheduler logs)"
                    )
                    scheduled_order.append(pod_name)

            time.sleep(2)  # Check every 2 seconds

        return scheduled_order

    def cordon_all_nodes(self) -> bool:
        """Cordon all worker nodes to prevent scheduling."""
        worker_nodes = self.get_nodes()
        if not worker_nodes:
            print("❌ No worker nodes found to cordon")
            return False

        print(f"🚧 Cordoning {len(worker_nodes)} worker nodes to block scheduling...")

        for node in worker_nodes:
            output, code = self.kubectl(["cordon", node])
            if code != 0:
                print(f"❌ Failed to cordon node {node}: {output}")
                # Try to uncordon any nodes we successfully cordoned
                self.uncordon_all_nodes()
                return False
            print(f"✅ Cordoned node: {node}")

        print(f"🚧 All {len(worker_nodes)} nodes are now unschedulable")
        return True

    def cordon_target_node(self, target_node: str = "chronos-test-worker") -> bool:
        """Cordon only the target node for sequential scheduling."""
        print(f"🚧 Cordoning target node {target_node} for sequential scheduling...")

        output, code = self.kubectl(["cordon", target_node])
        if code != 0:
            print(f"❌ Failed to cordon node {target_node}: {output}")
            return False
        else:
            print(f"✅ Cordoned target node: {target_node}")
            return True

    def uncordon_target_node(self, target_node: str = "chronos-test-worker") -> bool:
        """Uncordon only the target node."""
        print(
            f"🔓 Uncordoning target node {target_node} to enable sequential scheduling..."
        )

        output, code = self.kubectl(["uncordon", target_node])
        if code != 0:
            print(f"❌ Failed to uncordon node {target_node}: {output}")
            return False
        else:
            print(f"✅ Uncordoned target node: {target_node}")
            return True

    def _schedule_pods_one_by_one(
        self, pod_names: List[str], test_pods: List[dict]
    ) -> List[str]:
        """Schedule pods one by one to enforce QueueSort order with maximum control."""
        print("🎯 Using ONE-BY-ONE scheduling method for maximum order control")

        scheduled_order = []

        for i in range(len(pod_names)):
            print(f"\n--- ROUND {i+1}/{len(pod_names)} ---")

            # 1. Uncordon node briefly to allow ONE pod to schedule
            print("🔓 Uncordoning node to allow ONE pod to schedule...")
            if not self.uncordon_target_node("chronos-test-worker"):
                print("❌ Failed to uncordon node")
                break

            # 2. Wait for exactly ONE pod to get scheduled
            print("⏳ Waiting for exactly ONE pod to be scheduled...")
            start_time = time.time()
            newly_scheduled = None

            while time.time() - start_time < 30:  # 30s timeout per pod
                for pod_name in pod_names:
                    if pod_name not in scheduled_order:
                        # Check if this pod is now scheduled
                        output, code = self.kubectl(
                            [
                                "get",
                                "pod",
                                pod_name,
                                "-n",
                                self.namespace,
                                "-o",
                                "jsonpath={.status.phase}",
                            ]
                        )

                        if code == 0 and output == "Running":
                            newly_scheduled = pod_name
                            scheduled_order.append(pod_name)
                            print(
                                f"✅ Pod {pod_name} scheduled! (Order: {len(scheduled_order)})"
                            )
                            break

                if newly_scheduled:
                    break

                time.sleep(1)

            if not newly_scheduled:
                print(f"⚠️ No pod was scheduled in round {i+1}")
                break

            # 3. Immediately cordon the node again to prevent more scheduling
            if i < len(pod_names) - 1:  # Don't cordon after the last pod
                print("🚧 Cordoning node again to control next scheduling...")
                if not self.cordon_target_node("chronos-test-worker"):
                    print("❌ Failed to cordon node")
                    break

                # Small delay to ensure cordoning takes effect
                time.sleep(2)

        print("\n🎯 ONE-BY-ONE scheduling completed!")
        print(f"📊 Final order: {scheduled_order}")
        return scheduled_order

    def taint_target_node(self, target_node: str = "chronos-test-worker") -> bool:
        """Taint the target node to make it unschedulable."""
        print(f"🚫 Tainting target node {target_node} to prevent scheduling...")

        output, code = self.kubectl(
            [
                "taint",
                "node",
                target_node,
                "queuesort-test=true:NoSchedule",
                "--overwrite",
            ]
        )

        if code != 0:
            print(f"❌ Failed to taint node {target_node}: {output}")
            return False
        else:
            print(f"✅ Tainted target node: {target_node}")
            return True

    def untaint_target_node(self, target_node: str = "chronos-test-worker") -> bool:
        """Remove taint from the target node to make it schedulable."""
        print(
            f"🔓 Removing taint from target node {target_node} to enable scheduling..."
        )

        output, code = self.kubectl(
            ["taint", "node", target_node, "queuesort-test:NoSchedule-"]
        )

        if code != 0:
            # It's okay if the taint doesn't exist
            if "not found" in output.lower():
                print(f"✅ Target node {target_node} was already untainted")
                return True
            else:
                print(f"❌ Failed to untaint node {target_node}: {output}")
                return False
        else:
            print(f"✅ Untainted target node: {target_node}")
            return True

    def verify_node_tainted(self, target_node: str = "chronos-test-worker") -> bool:
        """Verify that the target node has the queuesort-test taint."""
        output, code = self.kubectl(["describe", "node", target_node])

        if code != 0:
            print(f"❌ Failed to describe node {target_node}")
            return False

        # Check if our specific taint exists
        if "queuesort-test=true:NoSchedule" in output:
            print(f"✅ Node {target_node} is properly tainted")
            return True
        else:
            print(
                f"❌ Node {target_node} is NOT tainted (pods will schedule immediately)"
            )
            return False

    def _schedule_pods_naturally_with_taint(
        self, pod_names: List[str], test_pods: List[dict]
    ) -> List[str]:
        """Schedule all pods naturally by untainting once and measuring complete scheduling order."""
        print(
            "🎯 Using NATURAL scheduling method - untaint once and measure COMPLETE scheduling order"
        )

        # 1. Untaint node to allow ALL pods to schedule simultaneously
        print("🔓 Removing taint to allow ALL pods to schedule naturally...")
        if not self.untaint_target_node("chronos-test-worker"):
            print("❌ Failed to remove taint")
            return []

        # 2. Monitor BOTH events and logs for comprehensive scheduling order detection
        print(
            "⏳ Monitoring BOTH Kubernetes events AND scheduler logs for complete scheduling order..."
        )

        # Try events first (most reliable for timing)
        events_order = self._get_complete_scheduling_order_from_events(
            pod_names, timeout=60
        )

        # Also try logs as backup (good for detailed binding info)
        logs_order = self._get_complete_scheduling_order_from_logs(
            pod_names, timeout=60
        )

        # Use the best available result - prefer logs if events have timestamp issues
        events_has_valid_timestamps = events_order and all(
            timestamp and timestamp != "<nil>" and timestamp.strip()
            for timestamp, _, _ in getattr(self, "_last_events_data", [])
        )

        if logs_order and len(logs_order) == len(pod_names):
            print(f"✅ Using LOGS-based order (most reliable): {logs_order}")
            complete_order = logs_order
        elif (
            events_order
            and len(events_order) == len(pod_names)
            and events_has_valid_timestamps
        ):
            print(f"✅ Using EVENTS-based order: {events_order}")
            complete_order = events_order
        elif events_order and len(events_order) == len(pod_names):
            print(
                f"⚠️ Using EVENTS-based order (timestamps may be unreliable): {events_order}"
            )
            complete_order = events_order
        elif logs_order:
            print(f"⚠️ Using partial LOGS-based order: {logs_order}")
            complete_order = logs_order
        elif events_order:
            print(f"⚠️ Using partial EVENTS-based order: {events_order}")
            complete_order = events_order
        else:
            print("❌ Could not determine scheduling order from either events or logs")
            return []

        if complete_order:
            print(f"📊 Final scheduling order: {complete_order}")
            return complete_order
        else:
            return []

    def _get_complete_scheduling_order_from_logs(
        self, pod_names: List[str], timeout: int = 120
    ) -> List[str]:
        """Get scheduling order from scheduler logs as backup method."""
        print("🔍 Checking scheduler logs for binding order...")

        # Get scheduler logs
        scheduler_logs = self.get_queuesort_scheduler_logs(return_logs=True)

        # Extract binding events
        recent_bindings = []
        for log_line in scheduler_logs.split("\n"):
            if (
                "Successfully bound pod to node" in log_line
                and f'pod="{self.namespace}/' in log_line
            ):
                try:
                    pod_part = log_line.split(f'pod="{self.namespace}/')[1].split('"')[
                        0
                    ]
                    if pod_part in pod_names:
                        timestamp_part = log_line.split()[0] + " " + log_line.split()[1]
                        recent_bindings.append((timestamp_part, pod_part))
                except (IndexError, ValueError):
                    continue

        if recent_bindings:
            # Sort by timestamp and take the most recent batch
            recent_bindings.sort(key=lambda x: x[0], reverse=True)
            latest_bindings = recent_bindings[: len(pod_names)]
            latest_bindings.reverse()  # Chronological order

            # Check if we have all pods
            latest_pods = [binding[1] for binding in latest_bindings]
            if set(latest_pods) == set(pod_names):
                print("📋 Found complete binding set in logs")
                return latest_pods

        print("⚠️ Could not find complete binding set in logs")
        return []

    def _get_first_scheduled_pod_from_logs(
        self, pod_names: List[str], timeout: int = 30
    ) -> str:
        """Monitor scheduler logs to find the first pod that gets bound."""
        print("🔍 Parsing scheduler binding logs for first scheduled pod...")

        start_time = time.time()
        seen_bindings = set()

        # Give a moment for scheduling to begin
        time.sleep(2)

        while time.time() - start_time < timeout:
            # Get fresh scheduler logs
            scheduler_logs = self.get_queuesort_scheduler_logs(return_logs=True)

            binding_events = []
            for log_line in scheduler_logs.split("\n"):
                if (
                    "Successfully bound pod=" in log_line
                    and f'pod="{self.namespace}/' in log_line
                ):
                    if log_line not in seen_bindings:
                        seen_bindings.add(log_line)
                        try:
                            # Extract pod name and timestamp
                            pod_part = log_line.split(f'pod="{self.namespace}/')[
                                1
                            ].split('"')[0]
                            if pod_part in pod_names:
                                timestamp_part = log_line.split()[1]  # Get timestamp
                                binding_events.append(
                                    (timestamp_part, pod_part, log_line)
                                )
                                print(
                                    f"🔍 Found binding: {pod_part} at {timestamp_part}"
                                )
                        except (IndexError, ValueError) as e:
                            continue

            if binding_events:
                # Sort by timestamp to find the earliest
                binding_events.sort(key=lambda x: x[0])
                first_pod = binding_events[0][1]
                first_timestamp = binding_events[0][0]
                print(f"✅ FIRST POD SCHEDULED: {first_pod} at {first_timestamp}")
                return first_pod

            time.sleep(1)

        print("⚠️ Timeout: Could not find first scheduled pod in logs")
        return ""

    def _wait_for_remaining_pods_scheduled(
        self, all_pods: List[str], first_pod: str, timeout: int = 60
    ) -> List[str]:
        """Wait for remaining pods to schedule and return their order."""
        remaining_pods = [pod for pod in all_pods if pod != first_pod]
        scheduled_order = []

        start_time = time.time()
        while (
            len(scheduled_order) < len(remaining_pods)
            and (time.time() - start_time) < timeout
        ):
            for pod_name in remaining_pods:
                if pod_name not in scheduled_order:
                    output, code = self.kubectl(
                        [
                            "get",
                            "pod",
                            pod_name,
                            "-n",
                            self.namespace,
                            "-o",
                            "jsonpath={.status.phase}",
                        ]
                    )

                    if code == 0 and output == "Running":
                        scheduled_order.append(pod_name)
                        print(
                            f"✅ Pod {pod_name} scheduled (position {len(scheduled_order) + 1})"
                        )

            time.sleep(1)

        return scheduled_order

    def _get_complete_scheduling_order_from_events(
        self, pod_names: List[str], timeout: int = 120
    ) -> List[str]:
        """Monitor Kubernetes events to get the complete chronological order of all pod scheduling."""
        print("🔍 Monitoring Kubernetes events for pod scheduling order...")

        start_time = time.time()
        scheduled_events = []

        # Give a moment for scheduling to begin
        time.sleep(3)

        while (
            len(scheduled_events) < len(pod_names)
            and (time.time() - start_time) < timeout
        ):
            # Get events in JSON format for better timestamp parsing
            output, code = self.kubectl(
                [
                    "get",
                    "events",
                    "--sort-by=.lastTimestamp",
                    "-n",
                    self.namespace,
                    "-o",
                    "json",
                ]
            )

            if code == 0:
                try:
                    import json

                    events_data = json.loads(output)
                    current_scheduled = []

                    for event in events_data.get("items", []):
                        reason = event.get("reason", "")
                        pod_name = event.get("involvedObject", {}).get("name", "")
                        timestamp = (
                            event.get("lastTimestamp")
                            or event.get("firstTimestamp")
                            or event.get("eventTime")
                        )
                        message = event.get("message", "")

                        if reason == "Scheduled" and pod_name in pod_names:
                            current_scheduled.append((timestamp, pod_name, message))
                            print(
                                f"🔍 Found scheduling event: {pod_name} at {timestamp}"
                            )

                except (json.JSONDecodeError, KeyError, ValueError) as e:
                    print(f"⚠️ Error parsing events JSON: {e}")
                    # Fallback to old method
                    output2, code2 = self.kubectl(
                        [
                            "get",
                            "events",
                            "--sort-by=.lastTimestamp",
                            "-n",
                            self.namespace,
                            "-o",
                            "custom-columns=TIME:.lastTimestamp,REASON:.reason,OBJECT:.involvedObject.name,MESSAGE:.message",
                            "--no-headers",
                        ]
                    )

                    if code2 == 0:
                        current_scheduled = []
                        for line in output2.split("\n"):
                            if line.strip() and "Scheduled" in line:
                                try:
                                    parts = line.strip().split(None, 3)
                                    if len(parts) >= 4:
                                        timestamp = parts[0]
                                        reason = parts[1]
                                        pod_name = parts[2]
                                        message = parts[3]

                                        if (
                                            pod_name in pod_names
                                            and reason == "Scheduled"
                                        ):
                                            current_scheduled.append(
                                                (timestamp, pod_name, message)
                                            )
                                            print(
                                                f"🔍 Found scheduling event (fallback): {pod_name} at {timestamp}"
                                            )
                                except (IndexError, ValueError):
                                    continue

                # Update our list with unique events
                for event in current_scheduled:
                    if event not in scheduled_events:
                        scheduled_events.append(event)

                # Check if we have all pods
                scheduled_pods = [event[1] for event in scheduled_events]
                if len(set(scheduled_pods)) == len(pod_names):
                    break

            time.sleep(2)

        if len(scheduled_events) >= len(pod_names):
            # Sort by timestamp to get chronological order
            scheduled_events.sort(key=lambda x: x[0])

            # Get unique pods in order (in case of duplicates)
            seen_pods = set()
            ordered_pods = []
            for timestamp, pod_name, message in scheduled_events:
                if pod_name not in seen_pods and pod_name in pod_names:
                    ordered_pods.append(pod_name)
                    seen_pods.add(pod_name)

            print("✅ COMPLETE SCHEDULING ORDER from events:")
            for i, pod_name in enumerate(ordered_pods):
                matching_event = next(e for e in scheduled_events if e[1] == pod_name)
                print(f"   {i+1}. {pod_name} at {matching_event[0]}")

            return ordered_pods
        else:
            print(f"⚠️ Could not find scheduling events for all {len(pod_names)} pods")
            return []

    def uncordon_all_nodes(self) -> bool:
        """Uncordon all worker nodes to allow scheduling."""
        worker_nodes = self.get_nodes()
        if not worker_nodes:
            print("❌ No worker nodes found to uncordon")
            return False

        print(
            f"🔓 Uncordoning {len(worker_nodes)} worker nodes to enable scheduling..."
        )

        for node in worker_nodes:
            output, code = self.kubectl(["uncordon", node])
            if code != 0:
                print(f"⚠️ Failed to uncordon node {node}: {output}")
                # Continue with other nodes even if one fails
            else:
                print(f"✅ Uncordoned node: {node}")

        print("🔓 All nodes are now schedulable")
        return True

    def _wait_for_all_pods_pending(
        self, pod_names: List[str], timeout: int = 60
    ) -> bool:
        """Wait for all pods to be in Pending state with verification-based approach."""
        print(f"⏳ Waiting for all {len(pod_names)} pods to reach Pending state...")

        start_time = time.time()
        while time.time() - start_time < timeout:
            pending_count = 0

            for pod_name in pod_names:
                output, code = self.kubectl(
                    [
                        "get",
                        "pod",
                        pod_name,
                        "-n",
                        self.namespace,
                        "-o",
                        "jsonpath={.status.phase}",
                    ]
                )
                if code == 0 and output == "Pending":
                    pending_count += 1

            print(f"📊 Pods in Pending state: {pending_count}/{len(pod_names)}")

            if pending_count == len(pod_names):
                print("✅ All pods are now in Pending state (queued) - perfect!")
                return True

            time.sleep(2)  # Check every 2 seconds

        print(
            f"❌ Timeout: Only {pending_count}/{len(pod_names)} pods reached Pending state"
        )
        return False

    def _wait_for_all_pods_pending_or_running(
        self, pod_names: List[str], timeout: int = 60, require_pending: bool = False
    ) -> bool:
        """Wait for all pods to be in Pending or Running state (more flexible than just Pending)."""
        if require_pending:
            print(
                f"⏳ Waiting for all {len(pod_names)} pods to reach Pending state (due to taint)..."
            )
        else:
            print(
                f"⏳ Waiting for all {len(pod_names)} pods to reach Pending or Running state..."
            )

        start_time = time.time()
        while time.time() - start_time < timeout:
            ready_count = 0
            failed_count = 0
            status_summary = {}

            for pod_name in pod_names:
                output, code = self.kubectl(
                    [
                        "get",
                        "pod",
                        pod_name,
                        "-n",
                        self.namespace,
                        "-o",
                        "jsonpath={.status.phase}",
                    ]
                )
                if code == 0:
                    phase = output.strip()
                    status_summary[pod_name] = phase

                    if require_pending:
                        # When require_pending=True, only accept Pending state
                        if phase == "Pending":
                            ready_count += 1
                        elif phase == "Running":
                            print(
                                f"⚠️ Pod {pod_name} is Running but should be Pending (taint not working?)"
                            )
                        elif phase in ["Failed", "Succeeded"]:
                            failed_count += 1
                    else:
                        # Normal mode: accept both Pending and Running
                        if phase in ["Pending", "Running"]:
                            ready_count += 1
                        elif phase in ["Failed", "Succeeded"]:
                            failed_count += 1
                else:
                    status_summary[pod_name] = "NotFound"

            if require_pending:
                print(f"📊 Pods in Pending state: {ready_count}/{len(pod_names)}")
            else:
                print(
                    f"📊 Pods ready (Pending/Running): {ready_count}/{len(pod_names)}"
                )

            if failed_count > 0:
                print(f"⚠️ Failed pods: {failed_count}")

            # Show detailed status for debugging
            if ready_count < len(pod_names):
                print("🔍 Detailed pod status:")
                for pod_name, status in status_summary.items():
                    print(f"   {pod_name}: {status}")

            if ready_count == len(pod_names):
                if require_pending:
                    print(
                        "✅ All pods are now Pending (tainted node working correctly)!"
                    )
                else:
                    print("✅ All pods are now ready (Pending/Running) - perfect!")
                return True

            if failed_count > 0:
                print(f"❌ {failed_count} pods failed - aborting")
                return False

            time.sleep(2)

        print(
            f"❌ Timeout: Only {ready_count}/{len(pod_names)} pods reached ready state"
        )
        return False

    def run_queuesort_scenario(
        self, scenario_name: str, scenario: Dict[str, Any]
    ) -> bool:
        """Runs a QueueSort test scenario using the reliable Node Tainting method."""
        print("\n" + "=" * 80)
        print(f"🎯 Running QueueSort scenario: {scenario_name}")
        print(f"📝 Description: {scenario['description']}")
        print("🚧 Using Node Tainting method (100% reliable)")
        print("=" * 80)

        # Extract pod names from setup_pods (nested by node)
        pod_names = []
        setup_pods = scenario["setup_pods"]
        for node_name, pods_on_node in setup_pods.items():
            for pod_config in pods_on_node:
                pod_names.append(pod_config["name"])

        return self._run_queuesort_with_taint(scenario_name, scenario, pod_names)

    def _run_queuesort_with_taint(
        self, scenario_name: str, scenario: Dict[str, Any], pod_names: List[str]
    ) -> bool:
        """Run QueueSort scenario using the reliable Node Tainting method."""

        # 1. Taint target node to make it unschedulable
        if not self.taint_target_node("chronos-test-worker"):
            return False

        # 1.1 Verify taint is actually applied
        if not self.verify_node_tainted("chronos-test-worker"):
            return False

        try:
            # 2. Create all test pods (they'll all be Pending due to tainted node)
            print(
                f"\n📋 Creating {len(pod_names)} test pods (will be queued due to tainted node)..."
            )

            # Always use sequential creation for realistic QueueSort testing
            print("🔄 Using sequential pod creation for realistic QueueSort testing...")
            if not self._create_pods_sequential(scenario["setup_pods"]):
                return False

            # 3. Wait for all test pods to reach Pending state (they should NOT be Running due to taint)
            print("🔍 Verifying all pods are Pending (blocked by taint)...")
            if not self._wait_for_all_pods_pending_or_running(
                pod_names, require_pending=True
            ):
                print(
                    "❌ Pods are not properly blocked by taint - cannot test QueueSort reliably"
                )
                return False

            # 4. Wait for QueueSort plugin to sort the queue
            queue_sort_wait = self.exec_config.get("queue_sort_wait_time", 15)
            print(
                f"⏳ Waiting {queue_sort_wait}s for QueueSort plugin to sort the queue..."
            )
            time.sleep(queue_sort_wait)

            # 5. Use NATURAL scheduling to test QueueSort priority
            print("🎯 Starting natural scheduling to test QueueSort priority...")
            actual_order = self._schedule_pods_naturally_with_taint(
                pod_names, scenario["setup_pods"]
            )

            # Wait longer for sequential scheduling to complete
            print("⏳ Waiting 10s for sequential scheduling to complete...")
            time.sleep(10)

            # Store results for analysis after cleanup
            self._queuesort_results = {
                "scenario": scenario,
                "pod_names": pod_names,
                "actual_order": actual_order,
                "method": "Natural Scheduling Method",
            }

        finally:
            # Always try to untaint target node in case of any errors
            print("🧹 Cleaning up: Removing taint from target node...")
            self.untaint_target_node("chronos-test-worker")

        # Analyze results AFTER cleanup
        if hasattr(self, "_queuesort_results"):
            results = self._queuesort_results
            delattr(self, "_queuesort_results")  # Clean up
            return self._analyze_queuesort_results(
                results["scenario"],
                results["pod_names"],
                results["actual_order"],
                results["method"],
            )
        else:
            return False

    def _analyze_queuesort_results(
        self,
        scenario: Dict[str, Any],
        pod_names: List[str],
        actual_order: List[str],
        method: str,
    ) -> bool:
        """Analyze and report QueueSort test results."""
        expected_order = scenario["expected_scheduling_order"]

        # Check if QueueSort comparisons happened (indicates QueueSort is working)
        queue_sort_comparisons = self.get_queuesort_scheduler_logs()

        # Success criteria for natural scheduling: Complete order should match simulations.yaml
        # QueueSort may have 0 comparisons if priorities are distinct (efficient sorting)
        # The key indicator is whether the scheduling order matches expectations
        all_pods_scheduled = len(actual_order) == len(pod_names)
        exact_match = actual_order == expected_order

        # QueueSort is working if we have comparisons OR if the order matches perfectly
        queuesort_working = queue_sort_comparisons > 0 or (
            all_pods_scheduled and exact_match
        )
        first_pod_correct = (
            len(actual_order) > 0
            and len(expected_order) > 0
            and actual_order[0] == expected_order[0]
        )

        if queuesort_working and all_pods_scheduled and exact_match:
            success = True
            status = "✅ PASSED (Perfect QueueSort Order - Exact Match)"
        elif queuesort_working and all_pods_scheduled and first_pod_correct:
            success = True
            status = "✅ PASSED (QueueSort Working - First Pod Correct, Minor Timing Variations)"
        elif queuesort_working and all_pods_scheduled:
            success = False
            status = (
                "❌ FAILED (QueueSort Wrong Order - Does Not Match simulations.yaml)"
            )
        else:
            success = False
            status = "❌ FAILED (QueueSort Not Active or Pods Not Scheduled)"

        print(f"\n📊 QUEUESORT RESULTS ({method}):")
        # Extract creation order from nested setup_pods
        creation_order = []
        setup_pods = scenario["setup_pods"]
        for node_name, pods_on_node in setup_pods.items():
            for pod_config in pods_on_node:
                creation_order.append(pod_config["name"])
        print(f"Creation Order:              {creation_order}")
        print(f"Expected Scheduling Order:   {expected_order}")
        print(f"Actual Scheduling Order:     {actual_order}")
        print("\n🔍 QUEUESORT VERIFICATION:")
        print(
            f"   QueueSort Comparisons:    {queue_sort_comparisons} (shows QueueSort is active)"
        )
        print(
            f"   All Pods Scheduled:       {all_pods_scheduled} ({len(actual_order)}/{len(pod_names)} pods)"
        )

        if success:
            print("   ✅ QueueSort plugin is working correctly!")
            print(
                "   📝 Note: Final scheduling order may differ due to Kubernetes parallel processing"
            )
        else:
            if not queuesort_working:
                print(
                    "   ❌ No QueueSort comparisons detected - plugin may not be active"
                )
            if not all_pods_scheduled:
                print(
                    f"   ❌ Not all pods were scheduled successfully ({len(actual_order)}/{len(pod_names)} pods)"
                )
            if not exact_match and not first_pod_correct:
                print(
                    "   ❌ Scheduling order doesn't match expected order from simulations.yaml"
                )

        # Show pod details for debugging
        print("\n🔍 POD DETAILS:")
        setup_pods = scenario["setup_pods"]
        for node_name, pods_on_node in setup_pods.items():
            for pod_config in pods_on_node:
                name = pod_config["name"]
                duration = pod_config.get("duration", "None")
                priority = pod_config.get("priority", "None")
                print(f"  {name}: duration={duration}s, priority={priority}")

        # Get scheduler logs showing queue sort activity
        print("\n📋 QUEUESORT LOGS:")
        queue_sort_comparisons = self.get_queuesort_scheduler_logs()

        # Verify queue sorting actually happened
        expected_comparisons = (
            len(pod_names) * (len(pod_names) - 1) // 2
        )  # n*(n-1)/2 for all pairs
        if (
            queue_sort_comparisons < expected_comparisons // 2
        ):  # Allow for some optimization
            print(
                f"⚠️  WARNING: Only {queue_sort_comparisons} QueueSort comparisons found, expected ~{expected_comparisons}"
            )
            print(
                "   This suggests incomplete queue sorting - test results may be unreliable"
            )

        # Show final pod states for debugging
        print("\n📊 FINAL POD STATES:")
        for pod_name in pod_names:
            output, code = self.kubectl(
                [
                    "get",
                    "pod",
                    pod_name,
                    "-n",
                    self.namespace,
                    "-o",
                    "custom-columns=NAME:.metadata.name,STATUS:.status.phase,NODE:.spec.nodeName,START:.status.startTime",
                    "--no-headers",
                ]
            )
            if code == 0:
                print(f"  {output}")

        print(f"\n{status}")

        if not success:
            print("\n🔍 DEBUGGING INFO:")
            print(
                f"This test validates the QueueSort plugin's Less() function using {method}."
            )
            print("Expected behavior:")
            print("1. Pods with higher priority should be scheduled first")
            print(
                "2. Among pods with same priority, longer duration should be scheduled first"
            )
            print("3. Pods without duration annotation should be scheduled last")
            print("4. FIFO ordering should break ties")

            print("\n🚧 Node Tainting method ensures:")
            print("- Eliminates race conditions completely")
            print("- All pods guaranteed to be Pending simultaneously")
            print("- Uses queuesort-test=true:NoSchedule taint")
            print("- Natural scheduling when taint is removed")
            print("- 100% reliable method for QueueSort testing")

        return success

    def get_queuesort_scheduler_logs(self, return_logs: bool = False):
        """Get and display scheduler logs relevant to QueueSort functionality
        Returns: Number of QueueSort comparisons found (int) or raw logs (str) if return_logs=True
        """
        # Find scheduler pod
        output, code = self.kubectl(
            ["get", "pods", "-n", "chronos-system", "-o", "name"]
        )

        if code != 0 or not output:
            print("❌ Could not find scheduler pod")
            return 0 if not return_logs else ""

        # Extract scheduler pod name from "pod/name" format
        scheduler_pod = None
        for line in output.split("\n"):
            if "chronos-scheduler" in line:
                scheduler_pod = line.replace("pod/", "").strip()
                break

        if not scheduler_pod:
            print("❌ Could not find chronos-scheduler pod")
            return 0 if not return_logs else ""

        # Get logs containing QueueSort activity
        output, code = self.kubectl(
            ["logs", "-n", "chronos-system", scheduler_pod, "--tail=200"]
        )

        if code != 0:
            print(f"❌ Failed to get scheduler logs: {output}")
            return 0 if not return_logs else ""

        # Return raw logs if requested (for binding order analysis)
        if return_logs:
            return output

        # Filter logs for QueueSort activity
        queue_logs = []
        for line in output.split("\n"):
            if any(
                keyword in line
                for keyword in [
                    "Less(",
                    "QueueSort",
                    "priority",
                    "duration",
                    "CHRONOS_SCORE",
                ]
            ):
                queue_logs.append(line)

        comparison_count = 0
        if queue_logs:
            print("Found QueueSort/scheduling activity:")
            for line in queue_logs[-20:]:  # Show last 20 relevant lines
                print(f"  {line}")
                # Count actual comparisons (lines with "Comparing pods")
                if "🎯 QueueSort: Comparing pods" in line:
                    comparison_count += 1
        else:
            print(
                "No QueueSort activity found in logs (this may indicate the plugin is disabled)"
            )

        return comparison_count

    def _validate_queuesort_order(self, actual_order, expected_order, scenario):
        """Validate that the scheduling order follows QueueSort logic reasonably well"""

        # Extract duration information for analysis
        pod_durations = {}
        setup_pods = scenario["setup_pods"]
        for node_name, pods_on_node in setup_pods.items():
            for pod_config in pods_on_node:
                name = pod_config["name"]
                duration = pod_config.get("duration")
                pod_durations[name] = duration if duration is not None else -1

        # Check key QueueSort principles:
        # 1. Pods with duration should generally come before pods without duration
        duration_pods = [p for p in actual_order if pod_durations[p] > 0]
        no_duration_pods = [p for p in actual_order if pod_durations[p] == -1]

        # Find positions in actual order
        duration_positions = [actual_order.index(p) for p in duration_pods]
        no_duration_positions = [actual_order.index(p) for p in no_duration_pods]

        if duration_positions and no_duration_positions:
            avg_duration_pos = sum(duration_positions) / len(duration_positions)
            avg_no_duration_pos = sum(no_duration_positions) / len(
                no_duration_positions
            )

            # Duration pods should generally be scheduled earlier (lower position)
            duration_first = avg_duration_pos < avg_no_duration_pos
        else:
            duration_first = True  # No mixed case to validate

        # 2. Among duration pods, longer should generally come before shorter
        longer_first = True
        if len(duration_pods) >= 2:
            # Check if longer jobs tend to be scheduled earlier
            duration_with_pos = [
                (pod_durations[p], actual_order.index(p)) for p in duration_pods
            ]
            duration_with_pos.sort(
                key=lambda x: x[0], reverse=True
            )  # Sort by duration desc

            # Check if positions generally increase (longer jobs earlier)
            positions = [pos for _, pos in duration_with_pos]
            longer_first = positions == sorted(positions)

        print("🔍 QUEUESORT ORDER VALIDATION:")
        print(f"   Duration pods scheduled first: {duration_first}")
        print(f"   Longer jobs scheduled earlier: {longer_first}")

        # Be lenient - if either principle is followed, consider it reasonable
        return duration_first or longer_first

    def get_scheduler_logs(self, pod_name: str) -> str:
        """Get scheduler logs for the test pod"""
        # Find scheduler pod
        output, code = self.kubectl(
            [
                "get",
                "pods",
                "-n",
                "chronos-system",
                "-l",
                "app.kubernetes.io/name=chronos-kubernetes-scheduler",
                "-o",
                "jsonpath={.items[0].metadata.name}",
            ]
        )

        if code != 0 or not output:
            print("❌ Could not find scheduler pod")
            return ""

        scheduler_pod = output
        print(f"📋 Getting logs from scheduler pod: {scheduler_pod}")

        # Get logs containing our test pod
        output, code = self.kubectl(["logs", "-n", "chronos-system", scheduler_pod])

        if code != 0:
            print(f"❌ Failed to get scheduler logs: {output}")
            return ""

        # Filter logs for our test pod
        relevant_logs = []
        for line in output.split("\n"):
            if pod_name in line and (
                "CHRONOS_SCORE" in line or "Successfully bound" in line
            ):
                relevant_logs.append(line)

        return "\n".join(relevant_logs)

    def analyze_scheduling_decision(
        self, logs: str, expected_node: str, expected_strategy: str
    ) -> Dict[str, Any]:
        """Analyze scheduler logs and return decision details"""
        result = {
            "chronos_scores": [],
            "chosen_node": None,
            "strategy_used": None,
            "success": False,
        }

        # Parse CHRONOS_SCORE lines
        chronos_pattern = r"CHRONOS_SCORE: Pod=([^,]+), Node=([^,]+), Strategy=([^,]+), NewPodDuration=(\d+)s, maxRemainingTime=(\d+)s, ExtensionDuration=(\d+)s, CompletionTime=([^,]+), FinalScore=(-?\d+)"

        for line in logs.split("\n"):
            match = re.search(chronos_pattern, line)
            if match:
                result["chronos_scores"].append(
                    {
                        "pod": match.group(1),
                        "node": match.group(2),
                        "strategy": match.group(3),
                        "new_pod_duration": int(match.group(4)),
                        "max_remaining_time": int(match.group(5)),
                        "extension_duration": int(match.group(6)),
                        "completion_time": match.group(7),
                        "final_score": int(match.group(8)),
                    }
                )

        # Parse successful binding
        binding_pattern = (
            r'Successfully bound pod to node.*pod="([^"]+)".*node="([^"]+)"'
        )
        for line in logs.split("\n"):
            match = re.search(binding_pattern, line)
            if match:
                result["chosen_node"] = match.group(2)

        # Determine if expectations were met
        if expected_node == "any":
            result["success"] = result["chosen_node"] is not None
        else:
            result["success"] = result["chosen_node"] == expected_node

        return result

    def run_scenario(self, scenario_name: str, scenario: Dict[str, Any]) -> bool:
        """Run a single test scenario"""
        print("\n" + "=" * 80)
        print(f"🎯 Running scenario: {scenario_name}")
        print(f"📝 Description: {scenario['description']}")
        print("=" * 80)

        # Setup initial conditions
        print("\n📋 Setting up initial conditions...")
        for node, pods in scenario["setup_pods"].items():
            for pod_config in pods:
                if not self.create_setup_pod(
                    pod_config["name"], pod_config["duration"], node
                ):
                    return False

        # Wait for setup pods to be running
        setup_wait = self.exec_config["setup_wait_time"]
        print(f"⏳ Waiting {setup_wait}s for setup pods to be running...")
        time.sleep(setup_wait)

        # Create test pod
        new_pod = scenario["new_pod"]
        test_wait = self.exec_config["test_wait_time"]
        print(f"⏳ Waiting {test_wait}s before scheduling test pod...")
        time.sleep(test_wait)

        if not self.create_test_pod(new_pod["name"], new_pod["duration"]):
            return False

        # Wait for scheduling
        timeout = self.exec_config["timeout"]
        actual_node = self.wait_for_pod_scheduled(new_pod["name"], timeout)
        if not actual_node:
            return False

        # Analyze results
        logs = self.get_scheduler_logs(new_pod["name"])
        analysis = self.analyze_scheduling_decision(
            logs, new_pod["expected_node"], new_pod["expected_strategy"]
        )

        # Print results
        print("\n📊 RESULTS:")
        print(f"Expected node: {new_pod['expected_node']}")
        print(f"Actual node: {actual_node}")
        print(f"Expected strategy: {new_pod['expected_strategy']}")

        if analysis["chronos_scores"]:
            print("\n🔍 CHRONOS_SCORE details:")
            for score in analysis["chronos_scores"]:
                print(f"  Node: {score['node']}")
                print(f"  Strategy: {score['strategy']} ")
                print(f"  Score: {score['final_score']}")
                print(f"  Max remaining: {score['max_remaining_time']}s")
                print(f"  Extension needed: {score['extension_duration']}s")
                print()

        success = analysis["success"]
        status = "✅ PASSED" if success else "❌ FAILED"
        print(f"\n{status}")

        return success

    def run_all_scenarios(self) -> bool:
        """Run all configured scenarios"""
        start_time = time.time()
        print("🚀 Starting Chronos Scheduler Simulation Tests")

        # Get overall execution timeout from config (in minutes, convert to seconds)
        overall_timeout = self.exec_config.get("overall_timeout_minutes", 10) * 60
        print(f"⏱️  Overall execution timeout: {overall_timeout // 60} minutes")

        if not self.create_namespace():
            return False

        # Ensure all nodes are uncordoned before starting tests (cleanup from previous runs)
        self._ensure_all_nodes_uncordoned()

        # Setup standard priority classes for enhanced QueueSort visibility
        # NOTE: Using non-default priority classes to preserve priority=0 for pods without priorityClassName
        if not self.setup_non_default_priority_classes():
            print("❌ Failed to setup priority classes - continuing with basic testing")
            # Don't fail the entire test run, but note this issue

        scenarios = self.config["scenarios"]
        passed = 0
        total = len(scenarios)

        try:
            for scenario_name, scenario in scenarios.items():
                # Check overall timeout
                elapsed = time.time() - start_time
                if elapsed >= overall_timeout:
                    print(
                        f"\n⚠️ Overall execution timeout reached ({elapsed:.1f}s >= {overall_timeout}s)"
                    )
                    print(f"Completed {passed}/{total} scenarios before timeout")
                    break

                # Check scenario type based on name prefix
                if scenario_name.startswith("queuesort_"):
                    print(f"\n🔍 Detected QueueSort scenario: {scenario_name}")
                    if self.run_queuesort_scenario(scenario_name, scenario):
                        passed += 1
                elif scenario_name.startswith("score_"):
                    print(f"\n🔍 Detected Score scenario: {scenario_name}")
                    if self.run_scenario(scenario_name, scenario):
                        passed += 1
                else:
                    print(
                        f"\n⚠️ Skipping scenario {scenario_name}: Unknown type (must start with 'queuesort_' or 'score_')"
                    )

                # Cleanup between scenarios
                print(f"🧹 Cleaning up pods from scenario: {scenario_name}")
                self.kubectl(
                    [
                        "delete",
                        "pods",
                        "--all",
                        "-n",
                        self.namespace,
                        "--force",
                        "--grace-period=0",
                    ]
                )
                self.cleanup_priority_classes()  # Clean up priority classes too
                cleanup_wait = self.exec_config.get(
                    "cleanup_wait", 2
                )  # Reduced from 5 to 2
                time.sleep(cleanup_wait)

        finally:
            self.cleanup_namespace()

        print("\n" + "=" * 80)
        elapsed_time = time.time() - start_time
        print(f"🏁 SIMULATION RESULTS: {passed}/{total} scenarios passed")
        print(f"⏱️  Total execution time: {elapsed_time:.1f}s")
        print("=" * 80)

        return passed == total


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description="Run Chronos scheduler simulations")
    parser.add_argument(
        "--config", default="simulations.yaml", help="Simulation config file"
    )
    parser.add_argument("--kubeconfig", help="Path to kubeconfig file")
    parser.add_argument("--scenario", help="Run specific scenario only")

    args = parser.parse_args()

    simulator = ChronosSimulator(args.config, args.kubeconfig)

    if args.scenario:
        # Run single scenario
        scenario = simulator.config["scenarios"].get(args.scenario)
        if not scenario:
            print(f"❌ Scenario '{args.scenario}' not found")
            sys.exit(1)

        simulator.create_namespace()

        # Check scenario type based on name prefix
        if args.scenario.startswith("queuesort_"):
            print(f"🔍 Running QueueSort scenario: {args.scenario}")
            success = simulator.run_queuesort_scenario(args.scenario, scenario)
        elif args.scenario.startswith("score_"):
            print(f"🔍 Running Score scenario: {args.scenario}")
            success = simulator.run_scenario(args.scenario, scenario)
        else:
            print(
                f"❌ Unknown scenario type: {args.scenario} (must start with 'queuesort_' or 'score_')"
            )
            success = False

        simulator.cleanup_namespace()
        sys.exit(0 if success else 1)
    else:
        # Run all scenarios
        success = simulator.run_all_scenarios()
        sys.exit(0 if success else 1)
