"""Скоринг-сервис: выбор последнего окна на скважину."""

from __future__ import annotations

import sys
from pathlib import Path

import pandas as pd

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))
from src.services.ml_scoring import latest_window_per_well  # noqa: E402


def test_latest_window_per_well_picks_max_window_end(tmp_path):
    d = tmp_path / "cache"
    d.mkdir()
    df1 = pd.DataFrame({
        "well_id": 1,
        "window_end": pd.to_datetime(["2025-01-01", "2025-01-03", "2025-01-02"]),
        "feat": [10, 30, 20],
    })
    df2 = pd.DataFrame({
        "well_id": 2,
        "window_end": pd.to_datetime(["2025-02-01", "2025-02-05"]),
        "feat": [1, 2],
    })
    df1.to_pickle(d / "well_1.pkl")
    df2.to_pickle(d / "well_2.pkl")

    out, history = latest_window_per_well(str(d))
    assert len(out) == 2
    # история возвращается целиком — по ней считаются признаки «отклонение
    # от собственного режима скважины»
    assert len(history) == 5
    row1 = out[out["well_id"] == 1].iloc[0]
    assert row1["window_end"] == pd.Timestamp("2025-01-03")
    assert row1["feat"] == 30
    row2 = out[out["well_id"] == 2].iloc[0]
    assert row2["window_end"] == pd.Timestamp("2025-02-05")


def test_latest_window_per_well_empty_cache(tmp_path):
    d = tmp_path / "cache"
    d.mkdir()
    out, history = latest_window_per_well(str(d))
    assert out.empty and history.empty


def test_own_baseline_uses_history_not_single_window():
    """Отклонение от своего режима на одном окне вырождается в ноль.

    Скоринг берёт последнее окно каждой скважины, и если базу считать по нему
    же, признак всегда равен нулю — модель, обученная на настоящих отклонениях,
    получила бы на инференсе константу.
    """
    from esp.pipeline.context import add_context_features

    history = pd.DataFrame({
        "well_id": [1] * 4,
        "oilfield": ["ng1"] * 4,
        "window_end": pd.to_datetime(["2025-01-01", "2025-01-02", "2025-01-03",
                                      "2025-01-04"]),
        "running_fraction": [1.0, 1.0, 1.0, 0.4],   # режим просел в последнем окне
        "n_pump_transitions": [0, 0, 0, 5],
    })
    latest = history.tail(1).reset_index(drop=True)
    man = {1: {"oilfield": "ng1", "failure_at": None}}

    without = add_context_features(latest, man)
    assert float(without["running_fraction_vs_own_base"].iloc[0]) == 0.0

    with_history = add_context_features(latest, man, history=history)
    assert float(with_history["running_fraction_vs_own_base"].iloc[0]) < -0.5
    assert float(with_history["n_pump_transitions_vs_own_base"].iloc[0]) == 5.0


def test_stage0_flags_do_not_suppress_tier():
    """Потеря связи обесценивает свежий замер, но не отменяет историю.

    Замолчавшая скважина не становится исправной: ярус опирается на все окна
    наблюдения, поэтому он сохраняется, а недостоверность помечается отдельно.
    """
    from esp.pipeline.cascade import stage0_rules

    bad, reasons = stage0_rules(pd.Series({"frozen_data": True,
                                           "insulation_alarm": True,
                                           "vfd_alarm": False}))
    assert bad is True
    assert any("связи" in r for r in reasons)
    assert any("изоляции" in r for r in reasons)

    ok, reasons_ok = stage0_rules(pd.Series({"frozen_data": False,
                                             "insulation_alarm": False,
                                             "vfd_alarm": False}))
    assert ok is False and reasons_ok == []
