"""Общие фикстуры для тестов. Данные синтетические, диск не читается
(кроме явного интеграционного теста на образце CSV)."""

from __future__ import annotations

from pathlib import Path

import numpy as np
import pandas as pd
import pytest

FIXTURES = Path(__file__).resolve().parent / "fixtures"
N = 240  # отсчётов в окне (минутная сетка)


def _index(n: int = N) -> pd.DatetimeIndex:
    return pd.date_range("2025-01-01", periods=n, freq="1min", tz="UTC")


@pytest.fixture
def rng() -> np.random.Generator:
    return np.random.default_rng(0)


@pytest.fixture
def normal_day(rng: np.random.Generator) -> pd.DataFrame:
    """Нормальная работа: каналы шумят вокруг рабочих значений (каноническ. имена)."""
    idx = _index()
    n = len(idx)
    return pd.DataFrame(
        {
            "F_out": 50.0 + rng.normal(0, 0.05, n),
            "Ped_load": 70.0 + rng.normal(0, 0.5, n),
            "Pump_intake_pressure_kgs": 50.0 + rng.normal(0, 0.3, n),
            "Motor_winding_temperature": 70.0 + rng.normal(0, 0.4, n),
        },
        index=idx,
    )


@pytest.fixture
def frozen_day() -> pd.DataFrame:
    """Залипшая телеметрия: все каналы константны весь период, но F_out>порога
    (установка «работает» — заморозку нельзя списать на простой)."""
    idx = _index()
    n = len(idx)
    return pd.DataFrame(
        {
            "F_out": np.full(n, 50.0),
            "Ped_load": np.full(n, 70.0),
            "Pump_intake_pressure_kgs": np.full(n, 50.0),
            "Motor_winding_temperature": np.full(n, 70.0),
        },
        index=idx,
    )


@pytest.fixture
def idle_day() -> pd.DataFrame:
    """Простой установки: F_out=0 и каналы константны — это ВАЛИДНО, не заморозка."""
    idx = _index()
    n = len(idx)
    return pd.DataFrame(
        {
            "F_out": np.zeros(n),
            "Ped_load": np.zeros(n),
            "Pump_intake_pressure_kgs": np.full(n, 45.0),
            "Motor_winding_temperature": np.full(n, 30.0),
        },
        index=idx,
    )


@pytest.fixture
def sample_csv_path() -> Path:
    return FIXTURES / "sample_day.csv"
