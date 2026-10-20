"""Regression checks for simulation polling, without a Kubernetes cluster."""

import importlib.util
import json
import unittest
from pathlib import Path
from unittest.mock import Mock, mock_open, patch

import yaml

SIMULATION_DIR = Path(__file__).parent
spec = importlib.util.spec_from_file_location(
    "simulations", SIMULATION_DIR / "run-simulations.py"
)
simulations = importlib.util.module_from_spec(spec)
spec.loader.exec_module(simulations)


def pod_response(**phases):
    return json.dumps(
        {
            "items": [
                {"metadata": {"name": name}, "status": {"phase": phase}}
                for name, phase in phases.items()
            ]
        }
    ), 0


def event_response(*bindings):
    return json.dumps(
        {
            "items": [
                {
                    "reason": "Scheduled",
                    "involvedObject": {"name": name},
                    "lastTimestamp": timestamp,
                }
                for name, timestamp in bindings
            ]
        }
    ), 0


class SimulationTests(unittest.TestCase):
    def setUp(self):
        self.simulator = simulations.ChronosSimulator(
            str(SIMULATION_DIR / "simulations.yaml")
        )
        self.simulator.kubectl = Mock()
        self.now = 0.0
        self.sleeps = []
        for context in [
            patch("builtins.print"),
            patch.object(simulations.time, "sleep", self.sleep),
            patch.object(simulations.time, "monotonic", lambda: self.now),
            patch.object(simulations.time, "time", lambda: self.now),
        ]:
            context.start()
            self.addCleanup(context.stop)

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds

    def test_batch_readiness_uses_one_request_for_eight_pods(self):
        phases = {f"pod-{i}": "Pending" for i in range(8)}
        self.simulator.kubectl.return_value = pod_response(**phases)

        self.assertTrue(
            self.simulator._wait_for_all_pods_pending_or_running(
                list(phases), require_pending=True
            )
        )
        self.simulator.kubectl.assert_called_once_with(
            ["get", "pods", "-n", "simulation-test", "-o", "json"]
        )
        self.assertEqual(self.sleeps, [])

    def test_missing_pod_requires_another_poll(self):
        self.simulator.kubectl.side_effect = [
            pod_response(first="Pending"),
            pod_response(first="Pending", second="Pending"),
        ]
        self.assertTrue(self.simulator._wait_for_all_pods_pending(["first", "second"]))
        self.assertEqual(self.simulator.kubectl.call_count, 2)
        self.assertEqual(self.sleeps, [1])

    def test_pending_check_rejects_running_pod(self):
        self.simulator.kubectl.return_value = pod_response(first="Running")
        self.assertFalse(
            self.simulator._wait_for_all_pods_pending_or_running(
                ["first"], timeout=0, require_pending=True
            )
        )

    def test_flexible_readiness_accepts_pending_and_running(self):
        self.simulator.kubectl.return_value = pod_response(
            first="Pending", second="Running", unrelated="Failed"
        )
        self.assertTrue(
            self.simulator._wait_for_all_pods_pending_or_running(["first", "second"])
        )

    def test_terminal_setup_pods_fail_without_waiting(self):
        for phase in ["Failed", "Succeeded"]:
            with self.subTest(phase=phase):
                self.simulator.kubectl.return_value = pod_response(first=phase)
                self.assertFalse(
                    self.simulator._wait_for_pod_phases(["first"], {"Running"})
                )
        self.assertEqual(self.sleeps, [])

    def test_transient_errors_are_retried(self):
        self.simulator.kubectl.side_effect = [
            ("API unavailable", 1),
            ("invalid json", 0),
            pod_response(first="Running"),
        ]
        self.assertTrue(self.simulator._wait_for_pod_phases(["first"], {"Running"}))
        self.assertEqual(self.sleeps, [1, 1])

    def test_readiness_timeout_caps_last_sleep(self):
        self.simulator.exec_config["polling_interval"] = 2
        self.simulator.kubectl.return_value = pod_response()
        self.assertFalse(
            self.simulator._wait_for_pod_phases(["first"], {"Running"}, timeout=3)
        )
        self.assertEqual(self.sleeps, [2, 1])
        self.assertEqual(self.now, 3)

    def test_empty_setup_does_not_poll(self):
        self.assertTrue(self.simulator._wait_for_pod_phases([], {"Running"}))
        self.simulator.kubectl.assert_not_called()

    def test_events_return_immediately_in_timestamp_order(self):
        self.simulator.kubectl.return_value = event_response(
            ("second", "2026-10-20T10:00:01Z"),
            ("first", "2026-10-20T10:00:00Z"),
            ("unrelated", "2026-10-20T09:00:00Z"),
        )
        self.assertEqual(
            self.simulator._get_complete_scheduling_order_from_events(
                ["second", "first"]
            ),
            ["first", "second"],
        )
        self.assertEqual(self.sleeps, [])

    def test_duplicate_events_do_not_end_polling_before_all_pods_schedule(self):
        self.simulator.kubectl.side_effect = [
            event_response(("first", "1"), ("first", "2")),
            event_response(("first", "1"), ("second", "3")),
        ]
        self.assertEqual(
            self.simulator._get_complete_scheduling_order_from_events(
                ["first", "second"]
            ),
            ["first", "second"],
        )
        self.assertEqual(self.simulator.kubectl.call_count, 2)

    def test_complete_logs_skip_event_wait(self):
        self.simulator.untaint_target_node = Mock(return_value=True)
        self.simulator._get_complete_scheduling_order_from_logs = Mock(
            return_value=["second", "first"]
        )
        self.simulator._get_complete_scheduling_order_from_events = Mock()
        self.assertEqual(
            self.simulator._schedule_pods_naturally_with_taint(["first", "second"], []),
            ["second", "first"],
        )
        self.simulator._get_complete_scheduling_order_from_events.assert_not_called()

    def test_binding_logs_include_full_current_scenario(self):
        self.simulator._queue_sort_log_since_time = "2026-10-20T10:00:00+00:00"
        self.simulator.kubectl.side_effect = [
            ("pod/chronos-scheduler-test", 0),
            ("binding logs", 0),
        ]
        self.assertEqual(
            self.simulator.get_queuesort_scheduler_logs(return_logs=True),
            "binding logs",
        )

        self.assertEqual(
            self.simulator.kubectl.call_args.args[0],
            [
                "logs",
                "-n",
                "chronos-system",
                "chronos-scheduler-test",
                "--since-time=2026-10-20T10:00:00+00:00",
            ],
        )

    def test_priority_commands_use_the_configured_kubeconfig(self):
        self.simulator.kubectl.return_value = ("created", 0)
        self.assertTrue(self.simulator.create_priority_class("test-priority-100", 100))
        self.assertNotIn("--kubeconfig", self.simulator.kubectl.call_args.args[0])
        self.simulator.kubectl.side_effect = [
            ("priorityclass.scheduling.k8s.io/test-priority-100", 0),
            ("deleted", 0),
        ]
        self.assertTrue(self.simulator.cleanup_priority_classes())
        for call in self.simulator.kubectl.call_args_list:
            self.assertNotIn("--kubeconfig", call.args[0])

    def test_log_order_uses_binding_timestamps(self):
        self.simulator.get_queuesort_scheduler_logs = Mock(
            return_value=(
                'I1020 10:00:02.000000 scheduler "Successfully bound pod to node" pod="simulation-test/second"\n'
                'I1020 10:00:01.000000 scheduler "Successfully bound pod to node" pod="simulation-test/first"'
            )
        )
        self.assertEqual(
            self.simulator._get_complete_scheduling_order_from_logs(
                ["second", "first"]
            ),
            ["first", "second"],
        )

    def test_incomplete_events_time_out_instead_of_accepting_duplicates(self):
        self.simulator.kubectl.return_value = event_response(
            ("first", "1"), ("first", "2")
        )
        self.assertEqual(
            self.simulator._get_complete_scheduling_order_from_events(
                ["first", "second"], timeout=3
            ),
            [],
        )
        self.assertEqual(self.now, 3)

    def test_events_remain_a_fallback_when_logs_are_missing(self):
        self.simulator.untaint_target_node = Mock(return_value=True)
        self.simulator._get_complete_scheduling_order_from_logs = Mock(return_value=[])
        self.simulator.kubectl.return_value = event_response(("first", "1"))
        self.assertEqual(
            self.simulator._schedule_pods_naturally_with_taint(["first"], []), ["first"]
        )

    def test_complete_binding_logs_still_take_precedence_over_events(self):
        self.simulator.untaint_target_node = Mock(return_value=True)
        self.simulator._get_complete_scheduling_order_from_logs = Mock(
            side_effect=[[], ["second", "first"]]
        )
        self.simulator.kubectl.return_value = event_response(
            ("first", "1"), ("second", "2")
        )
        self.assertEqual(
            self.simulator._schedule_pods_naturally_with_taint(["first", "second"], []),
            ["second", "first"],
        )

    def test_wrong_or_incomplete_queue_order_still_fails(self):
        scenario = self.simulator.config["scenarios"]["queuesort_longest_first"]
        self.simulator.get_queuesort_scheduler_logs = Mock(return_value=1)
        self.simulator.kubectl.return_value = ("", 0)
        expected = scenario["expected_scheduling_order"]
        for order in [list(reversed(expected)), expected[:-1]]:
            with self.subTest(order=order):
                self.assertFalse(
                    self.simulator._analyze_queuesort_results(
                        scenario, expected, order, "Natural Scheduling Method"
                    )
                )

    def test_score_scenario_waits_for_running_setup_without_fixed_delays(self):
        scenario = self.simulator.config["scenarios"]["score_bin_packing_perfect_fit"]
        self.simulator.create_setup_pod = Mock(return_value=True)
        self.simulator.create_test_pod = Mock(return_value=True)
        self.simulator.wait_for_pod_scheduled = Mock(
            return_value="chronos-test-worker2"
        )
        self.simulator.get_scheduler_logs = Mock(return_value="")
        self.simulator.analyze_scheduling_decision = Mock(
            return_value={"success": True, "chronos_scores": []}
        )
        phases = {
            pod["name"]: "Running"
            for pods in scenario["setup_pods"].values()
            for pod in pods
        }
        self.simulator.kubectl.return_value = pod_response(**phases)
        self.assertTrue(self.simulator.run_scenario("score_test", scenario))
        self.assertEqual(self.sleeps, [])

    def test_score_scenario_does_not_create_test_pod_after_setup_failure(self):
        scenario = self.simulator.config["scenarios"]["score_empty_node_avoidance"]
        self.simulator.create_setup_pod = Mock(return_value=True)
        self.simulator.create_test_pod = Mock()
        self.simulator.kubectl.return_value = pod_response(
            **{"existing-job-1": "Failed"}
        )
        self.assertFalse(self.simulator.run_scenario("score_test", scenario))
        self.simulator.create_test_pod.assert_not_called()

    def test_queuesort_keeps_settling_wait_and_removes_post_scheduling_wait(self):
        scenario = self.simulator.config["scenarios"]["queuesort_longest_first"]
        for method in [
            "taint_target_node",
            "verify_node_tainted",
            "_create_pods_sequential",
            "_wait_for_all_pods_pending_or_running",
            "untaint_target_node",
            "_analyze_queuesort_results",
        ]:
            setattr(self.simulator, method, Mock(return_value=True))
        order = scenario["expected_scheduling_order"]
        self.simulator._schedule_pods_naturally_with_taint = Mock(return_value=order)
        self.assertTrue(
            self.simulator._run_queuesort_with_taint("queuesort_test", scenario, order)
        )
        self.assertEqual(self.sleeps, [8])

    def test_pod_manifests_are_valid_and_reuse_cached_images(self):
        self.simulator.kubectl.return_value = ("created", 0)
        for method, args in [
            (self.simulator.create_setup_pod, ("setup", 60, "worker")),
            (self.simulator.create_test_pod, ("test", 30)),
            (self.simulator.create_queuesort_test_pod, ("queue", 30)),
        ]:
            with (
                self.subTest(method=method.__name__),
                patch("builtins.open", mock_open()) as file,
            ):
                self.assertTrue(method(*args))
                pod = yaml.safe_load(file().write.call_args.args[0])
                self.assertEqual(
                    pod["spec"]["containers"][0]["imagePullPolicy"], "IfNotPresent"
                )
        pod = yaml.safe_load(
            self.simulator._generate_queuesort_pod_yaml("queue", 60, None)
        )
        self.assertEqual(
            pod["spec"]["containers"][0]["imagePullPolicy"], "IfNotPresent"
        )


if __name__ == "__main__":
    unittest.main()
