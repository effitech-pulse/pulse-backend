"""Дрейф в контуре прогона: заполнение полей и fail-soft поведение.

Мониторинг не имеет права ломать скоринг: если профиля нет или расчёт упал,
прогон должен завершиться штатно, а вердикт остаться пустым.
"""

from __future__ import annotations

import sys
from pathlib import Path

import numpy as np
import pandas as pd

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from esp.monitoring import build_reference_profile  # noqa: E402
from src.models.ml_run import MlRun  # noqa: E402
from src.services.ml_store import _attach_drift  # noqa: E402

FEATS = ["a", "b"]


def _frame(n: int, shift: float = 0.0, seed: int = 0) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    return pd.DataFrame({"a": rng.normal(shift, 1, n), "b": rng.uniform(0, 1, n) + shift})


def _profile(n: int = 2000) -> dict:
    return build_reference_profile(_frame(n, seed=1), FEATS)


def test_stable_data_marked_stable() -> None:
    run = MlRun()
    _attach_drift(run, {"drift_profile": _profile(), "X": _frame(2000, seed=2)})
    assert run.drift_verdict == "stable"
    assert run.drift_psi_max < 0.10
    assert run.drift_top                      # перечислены признаки с их PSI


def test_shifted_data_marked_severe() -> None:
    run = MlRun()
    _attach_drift(run, {"drift_profile": _profile(), "X": _frame(2000, shift=2.0, seed=2)})
    assert run.drift_verdict == "severe"
    assert run.drift_psi_max >= 0.25


def test_missing_profile_leaves_verdict_empty() -> None:
    """Модель обучена до появления детектора — прогон не должен падать."""
    run = MlRun()
    _attach_drift(run, {"drift_profile": None, "X": _frame(100)})
    assert run.drift_verdict is None
    assert run.drift_psi_max is None


def test_empty_scoring_matrix_is_tolerated() -> None:
    run = MlRun()
    _attach_drift(run, {"drift_profile": _profile(), "X": pd.DataFrame()})
    assert run.drift_verdict is None


def test_broken_profile_does_not_raise() -> None:
    """Повреждённый профиль не валит прогон (fail-soft)."""
    run = MlRun()
    _attach_drift(run, {"drift_profile": {"edges": "не словарь"}, "X": _frame(100)})
    assert run.drift_verdict is None


def test_drift_top_is_compact() -> None:
    """Строка для интерфейса — не более пяти признаков."""
    run = MlRun()
    _attach_drift(run, {"drift_profile": _profile(), "X": _frame(2000, shift=1.0, seed=3)})
    assert run.drift_top.count(",") <= 4
