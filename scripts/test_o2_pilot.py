"""Проверки защитных границ локального пилота без Docker и живой БД."""
from __future__ import annotations

import concurrent.futures
import importlib.util
import fcntl
import multiprocessing
import threading
import time
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location("o2_pilot", Path(__file__).with_name("o2_pilot.py"))
assert SPEC and SPEC.loader
pilot = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(pilot)


def hold_budget_lock(path, ready, release):
    """Отдельный процесс удерживает реальный flock до проверенной остановки."""
    with Path(path).open("r+") as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        ready.set()
        release.wait(10)


class BudgetTests(unittest.TestCase):
    def test_concurrent_reservations_never_exceed_two(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "budget.json"
            pilot.initialize_budget(path)
            def reserve(_: int) -> str | None:
                try:
                    return pilot.reserve(path)
                except pilot.GateFailure:
                    return None
            with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                accepted = [token for token in pool.map(reserve, range(20)) if token]
            self.assertEqual(len(accepted), 2)
            self.assertEqual(pilot.budget_status(path)["requests"], 2)
            for token in accepted:
                pilot.release(path, token)
            self.assertEqual(pilot.budget_status(path)["inflight"], [])

    def test_two_hundred_is_aggregate_and_reinitialization_forbidden(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "budget.json"
            pilot.initialize_budget(path)
            for _ in range(200):
                pilot.release(path, pilot.reserve(path))
            with self.assertRaises(pilot.GateFailure):
                pilot.reserve(path)
            with self.assertRaises(pilot.GateFailure):
                pilot.initialize_budget(path)
            self.assertEqual(pilot.budget_status(path)["requests"], 200)

    def test_unknown_release_and_corrupt_budget_fail_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "budget.json"
            pilot.initialize_budget(path)
            with self.assertRaises(pilot.GateFailure):
                pilot.release(path, "not-a-lease")
            path.write_text('{"requests": -1}')
            with self.assertRaises(pilot.GateFailure):
                pilot.reserve(path)


class GuardTests(unittest.TestCase):
    def test_guard_limits_and_missing_measurement(self) -> None:
        for growth, free, elapsed, expected in [
            (0, 20 * 1024**3, 1, None),
            (1024**3, 20 * 1024**3, 1, "store_growth"),
            (0, 10 * 1024**3 - 1, 1, "host_disk"),
            (0, 20 * 1024**3, 1800, "time_expired"),
            (None, 20 * 1024**3, 1, "unobservable"),
        ]:
            with self.subTest(expected=expected):
                self.assertEqual(pilot.guard_reason(growth, free, elapsed, 1800), expected)

    def test_stop_command_contains_only_optional_services(self) -> None:
        command = pilot.stop_command("pulse-prep", Path("/private/tmp/private.env"))
        self.assertEqual(command[-8:], ["stop", "--timeout", "3", "otel-collector", "loki", "tempo", "prometheus", "grafana"])
        self.assertNotIn("down", command)
        self.assertNotIn("backend", command)
        self.assertNotIn("postgres", command)

    def test_supervisor_measurement_failure_stops_optional_and_reports_failure(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory) / "evidence.jsonl"
            with patch.object(pilot, "measure_stores", side_effect=pilot.GateFailure("unobservable")), patch.object(pilot, "run", side_effect=pilot.GateFailure("stop_failed")):
                result = pilot.supervise("pulse-prep", Path("/private/tmp/private.env"), evidence, Path(directory)/"budget", 1)
            self.assertEqual(result, 2)
            rows = [__import__("json").loads(row) for row in evidence.read_text().splitlines()]
            self.assertEqual(rows[0]["guard"], "unobservable")
            self.assertFalse(rows[-1]["optional_stop_succeeded"])
            self.assertFalse(rows[-1]["business_services_stopped"])

    def test_supervisor_evidence_open_failure_stops_optional(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory) / "evidence.jsonl"
            evidence.write_text("existing evidence")
            commands = []
            with patch.object(pilot, "run", side_effect=lambda command, timeout: commands.append(command) or ""):
                with self.assertRaises(pilot.GateFailure):
                    pilot.supervise("pulse-prep", Path("/private/tmp/private.env"), evidence, Path(directory)/"budget", 1)
            self.assertEqual(commands[0][-5:], ["otel-collector", "loki", "tempo", "prometheus", "grafana"])
            self.assertEqual(evidence.read_text(), "existing evidence")

    def test_held_process_lock_stops_supervisor_before_holder_unlocks(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "budget.json"
            evidence = Path(directory) / "evidence.jsonl"
            pilot.initialize_budget(path)
            context = multiprocessing.get_context("spawn")
            ready, unlock = context.Event(), context.Event()
            holder = context.Process(target=hold_budget_lock, args=(str(path), ready, unlock))
            stopped = threading.Event()
            results = []
            def stop(command, timeout):
                self.assertEqual(command[-5:], ["otel-collector", "loki", "tempo", "prometheus", "grafana"])
                stopped.set()
                return ""
            def supervise():
                results.append(pilot.supervise("pulse-prep", Path("/private/tmp/private.env"), evidence, path, 0.1))
            holder.start()
            worker = None
            try:
                self.assertTrue(ready.wait(3))
                with patch.object(pilot, "measure_stores", return_value=0), patch.object(pilot, "run", side_effect=stop):
                    started = time.monotonic()
                    worker = threading.Thread(target=supervise)
                    worker.start()
                    try:
                        self.assertTrue(stopped.wait(1.3), "guard waits on budget lock instead of stopping")
                        self.assertTrue(holder.is_alive())
                        self.assertFalse(unlock.is_set())
                        worker.join(0.3)
                        self.assertEqual(results, [2])
                        self.assertLess(time.monotonic() - started, 1.6)
                    finally:
                        unlock.set()
                        worker.join(3)
            finally:
                unlock.set()
                holder.join(3)
                if holder.is_alive():
                    holder.terminate()
                    holder.join()
            rows = [__import__("json").loads(row) for row in evidence.read_text().splitlines()]
            self.assertEqual(rows[0]["guard"], "unobservable")
            self.assertTrue(rows[-1]["optional_stop_succeeded"])


class MeasurerTests(unittest.TestCase):
    def test_run_failure_cleans_up_only_unique_owned_helper(self) -> None:
        commands = []
        def boundary(command, timeout=8):
            commands.append(command)
            if command[:2] == ["docker", "run"]:
                raise pilot.GateFailure("docker_measurement_or_stop_failed")
            return ""
        with patch.object(pilot, "run", side_effect=boundary):
            with self.assertRaises(pilot.GateFailure):
                pilot.run_measurer(["--network", "none", pilot.MEASURER, "sleep", "15"], timeout=0.05)
        name = commands[0][commands[0].index("--name") + 1]
        self.assertRegex(name, r"^pulse-o2-measurer-[a-f0-9]{32}$")
        self.assertIn(["docker", "rm", "--force", name], commands)
        self.assertNotIn("compose", [arg for command in commands for arg in command])

    def test_cleanup_failure_is_explicit_and_cannot_be_success(self) -> None:
        def boundary(command, timeout=8):
            if command[:2] == ["docker", "run"]:
                return "measurement"
            raise pilot.GateFailure("docker_measurement_or_stop_failed")
        with patch.object(pilot, "run", side_effect=boundary):
            with self.assertRaisesRegex(pilot.GateFailure, "cleanup_unconfirmed"):
                pilot.run_measurer(["--network", "none", pilot.MEASURER, "true"])

    def test_success_still_confirms_daemon_absence_after_auto_remove(self) -> None:
        commands = []
        def boundary(command, timeout=8):
            commands.append((command, timeout))
            if command[:2] == ["docker", "run"]:
                return "4\t/measure/loki\n"
            if command[:2] == ["docker", "rm"]:
                raise pilot.GateFailure("docker_measurement_or_stop_failed")
            return ""
        with patch.object(pilot, "run", side_effect=boundary):
            value = pilot.run_measurer(["--rm", "--network", "none", pilot.MEASURER, "true"])
        self.assertEqual(value, "4\t/measure/loki\n")
        name = commands[0][0][commands[0][0].index("--name") + 1]
        self.assertEqual(commands[-1][0], ["docker", "ps", "--all", "--filter", f"name=^/{name}$", "--format", "{{.Names}}"])
        self.assertTrue(all(timeout <= 3 for _, timeout in commands[1:]))


if __name__ == "__main__":
    unittest.main()
