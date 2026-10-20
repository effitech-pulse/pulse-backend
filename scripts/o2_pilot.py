"""Защитный supervisor и общий бюджет синтетического O2-пилота.

Root harness обязан reserve перед каждым synthetic business request и release в
finally. Это бюджет контроллера, а не ограничитель внешнего HTTP ingress.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import time
from typing import Any, Iterator
import uuid

ROOT = Path(__file__).resolve().parents[1]
SERVICES = ("otel-collector", "loki", "tempo", "prometheus", "grafana")
STORES = ("loki", "tempo", "prometheus", "grafana")
MEASURER = "grafana/grafana:13.2.4@sha256:dbbc38ea21103cefb50396340cd88afec779c7e070b0a7bf5a75b4ce46651d3f"


class GateFailure(RuntimeError):
    """Пилот не может безопасно продолжаться; текст не содержит внешних ошибок."""


@contextmanager
def locked_budget(path: Path) -> Iterator[dict[str, Any]]:
    """Обновить общий файл под межпроцессной блокировкой без сброса счётчиков."""
    try:
        with path.open("r+", encoding="utf-8") as stream:
            deadline = time.monotonic() + 0.5
            while True:
                if time.monotonic() >= deadline:
                    raise GateFailure("budget_lock_timeout")
                try:
                    fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise GateFailure("budget_lock_timeout") from None
                    time.sleep(min(0.01, remaining))
            state = json.load(stream)
            if (not isinstance(state, dict) or type(state.get("requests")) is not int
                    or not 0 <= state["requests"] <= 200
                    or not isinstance(state.get("inflight"), list)
                    or len(state["inflight"]) > 2
                    or any(not isinstance(v, str) or not re.fullmatch(r"[a-f0-9]{32}", v)
                           for v in state["inflight"])
                    or len(set(state["inflight"])) != len(state["inflight"])
                    or len(state["inflight"]) > state["requests"]):
                raise GateFailure("budget_invalid")
            yield state
            stream.seek(0)
            json.dump(state, stream, sort_keys=True)
            stream.truncate()
            stream.flush()
            os.fsync(stream.fileno())
    except (OSError, ValueError, TypeError):
        raise GateFailure("budget_unavailable") from None


def initialize_budget(path: Path) -> None:
    """Создать один новый приватный бюджет; существующий никогда не обнулять."""
    try:
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump({"requests": 0, "inflight": []}, stream)
    except OSError:
        raise GateFailure("budget_already_exists_or_unavailable") from None


def reserve(path: Path) -> str:
    """Зарезервировать запрос, учитывая одновременно все процессы harness."""
    with locked_budget(path) as state:
        if state["requests"] >= 200 or len(state["inflight"]) >= 2:
            raise GateFailure("workload_budget_exhausted")
        token = uuid.uuid4().hex
        state["requests"] += 1
        state["inflight"].append(token)
        return token


def release(path: Path, token: str) -> None:
    """Освободить concurrency slot, не возвращая израсходованный request budget."""
    with locked_budget(path) as state:
        if token not in state["inflight"]:
            raise GateFailure("unknown_lease")
        state["inflight"].remove(token)


def budget_status(path: Path) -> dict[str, Any]:
    """Прочитать проверенное состояние для отчёта."""
    with locked_budget(path) as state:
        return dict(state)


def guard_reason(growth: int | None, free: int | None, elapsed: float,
                 duration: float) -> str | None:
    """Проверить защитные пределы; отсутствие измерения закрывает gate."""
    if growth is None or free is None or growth < 0 or free < 0:
        return "unobservable"
    if growth >= 1024**3:
        return "store_growth"
    if free < 10 * 1024**3:
        return "host_disk"
    if elapsed >= duration:
        return "time_expired"
    return None


def compose_command(project: str, env_file: Path) -> list[str]:
    """Обращаться только к изолированному PREP проекту, без вывода env."""
    if project != "pulse-prep":
        raise GateFailure("only_pulse_prep_project_allowed")
    return ["docker", "compose", "--project-name", project, "--env-file", str(env_file),
            "-f", str(ROOT / "deployments/compose.prep.yaml"),
            "-f", str(ROOT / "deployments/compose.o1-f1.yaml"),
            "-f", str(ROOT / "deployments/compose.o2.yaml"), "--profile", "observability"]


def stop_command(project: str, env_file: Path) -> list[str]:
    """Остановить только пять optional контейнеров; apps/exporters остаются bounded."""
    return compose_command(project, env_file) + ["stop", "--timeout", "3", *SERVICES]


def run(command: list[str], timeout: float = 8) -> str:
    """Ограничить время Docker CLI и скрыть произвольный stderr от evidence."""
    try:
        result = subprocess.run(command, check=True, capture_output=True,
                                text=True, timeout=timeout)
        return result.stdout
    except (OSError, subprocess.SubprocessError):
        raise GateFailure("docker_measurement_or_stop_failed") from None


def cleanup_measurer(name: str) -> None:
    """Удалить только собственный helper и подтвердить отсутствие через daemon.

    Отсутствие подтверждается на момент lookup. Pending create RPC после timeout
    клиента всё ещё может завершиться позже; этот риск не скрывается в ошибке.
    """
    previous_mask = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGTERM})
    try:
        try:
            run(["docker", "rm", "--force", name], timeout=3)
        except GateFailure:
            # --rm may already have removed the helper. A successful daemon
            # lookup, not stderr parsing, must distinguish absence from failure.
            pass
        try:
            remaining = run(["docker", "ps", "--all", "--filter", f"name=^/{name}$",
                             "--format", "{{.Names}}"], timeout=3).strip()
        except GateFailure:
            raise GateFailure(f"measurer_cleanup_unconfirmed:{name}") from None
        if remaining:
            raise GateFailure(f"measurer_cleanup_unconfirmed:{name}")
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, previous_mask)


def run_measurer(arguments: list[str], timeout: float = 8) -> str:
    """Запустить named helper с bounded cleanup при успехе, ошибке и сигнале."""
    name = f"pulse-o2-measurer-{uuid.uuid4().hex}"
    try:
        try:
            return run(["docker", "run", "--name", name, *arguments], timeout=timeout)
        except GateFailure:
            raise GateFailure(f"measurer_failed:{name}:creation_completion_unknown") from None
    finally:
        cleanup_measurer(name)


def terminate_pilot(_signum: int, _frame: Any) -> None:
    """Первый SIGTERM завершает CLI через finally; повторный не прерывает cleanup."""
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    raise GateFailure("pilot_terminated")


def measure_stores(project: str) -> int:
    """Измерить allocated KiB четырёх volumes read-only одноразовым контейнером."""
    command = ["--rm", "--network", "none", "--read-only", "--user", "0",
               "--cap-drop", "ALL", "--cap-add", "DAC_READ_SEARCH",
               "--security-opt", "no-new-privileges:true",
               "--memory", "64m", "--cpus", "0.2", "--entrypoint", "/bin/sh"]
    for store in STORES:
        name = f"{project}_o2-{store}"
        # A missing volume must fail before docker run could create an empty one.
        run(["docker", "volume", "inspect", name])
        command.extend(["--mount", f"type=volume,src={name},dst=/measure/{store},readonly"])
    command += [MEASURER, "-ec", "du -sk /measure/loki /measure/tempo /measure/prometheus /measure/grafana"]
    lines = run_measurer(command).splitlines()
    try:
        if len(lines) != 4:
            raise ValueError
        return sum(int(line.split()[0]) for line in lines) * 1024
    except (ValueError, IndexError):
        raise GateFailure("volume_size_unobservable") from None


def supervise(project: str, env_file: Path, evidence: Path, budget: Path,
              duration: float) -> int:
    """Наблюдать каждые 5s и stop optional containers при любом guard failure."""
    compose_command(project, env_file)
    if not 0 < duration <= 1800:
        raise GateFailure("duration_out_of_bounds")
    started = time.monotonic()
    baseline: int | None = None
    reason: str | None = None
    try:
        evidence.parent.mkdir(parents=True, exist_ok=True)
        stream = evidence.open("x", encoding="utf-8")
    except OSError:
        try:
            run(stop_command(project, env_file), timeout=25)
        except GateFailure:
            raise GateFailure("evidence_unavailable_optional_stop_failed") from None
        raise GateFailure("evidence_unavailable_optional_stopped") from None
    with stream:
        try:
            while True:
                sample: dict[str, Any] = {"elapsed_seconds": round(time.monotonic() - started, 3)}
                try:
                    size = measure_stores(project)
                    free = shutil.disk_usage(evidence.parent).free
                    state = budget_status(budget)
                    baseline = size if baseline is None else baseline
                    growth = max(0, size - baseline)
                    sample.update(store_bytes=size, baseline_bytes=baseline, growth_bytes=growth,
                                  host_free_bytes=free, requests=state["requests"],
                                  inflight=len(state["inflight"]))
                    reason = guard_reason(growth, free, time.monotonic() - started, duration)
                except (GateFailure, OSError) as exc:
                    reason = "unobservable"
                    sample["measurement_failure"] = str(exc) if isinstance(exc, GateFailure) else "host_measurement_failed"
                sample["guard"] = reason or "continue"
                stream.write(json.dumps(sample, sort_keys=True) + "\n")
                stream.flush()
                os.fsync(stream.fileno())
                if reason:
                    break
                time.sleep(min(5, max(0, duration - (time.monotonic() - started))))
        finally:
            # Includes KeyboardInterrupt and unexpected evidence/disk errors.
            stopped = True
            try:
                run(stop_command(project, env_file), timeout=25)
            except GateFailure:
                stopped = False
            stream.write(json.dumps({"guard": reason or "interrupted", "optional_stop_succeeded": stopped,
                                     "business_services_stopped": False}) + "\n")
            stream.flush()
    return 0 if reason == "time_expired" and stopped else 2


def main() -> int:
    """CLI для supervisor и общего request/concurrency budget."""
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("budget-init", "reserve", "release", "status"):
        child = sub.add_parser(name)
        child.add_argument("--budget-file", type=Path, required=True)
        if name == "release":
            child.add_argument("--token", required=True)
    child = sub.add_parser("supervise")
    child.add_argument("--project", default="pulse-prep")
    child.add_argument("--env-file", type=Path, required=True)
    child.add_argument("--evidence", type=Path, required=True)
    child.add_argument("--budget-file", type=Path, required=True)
    child.add_argument("--duration-seconds", type=float, default=1800)
    args = parser.parse_args()
    signal.signal(signal.SIGTERM, terminate_pilot)
    try:
        if args.command == "budget-init":
            initialize_budget(args.budget_file)
        elif args.command == "reserve":
            print(reserve(args.budget_file))
        elif args.command == "release":
            release(args.budget_file, args.token)
        elif args.command == "status":
            print(json.dumps(budget_status(args.budget_file), sort_keys=True))
        else:
            return supervise(args.project, args.env_file, args.evidence,
                             args.budget_file, args.duration_seconds)
    except GateFailure as exc:
        print(json.dumps({"gate": "FAIL", "reason": str(exc)}))
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
