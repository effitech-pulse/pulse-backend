"""Загрузка файла телеметрии: валидация и скоринг (fail-soft, вердикты)."""

from __future__ import annotations

import sys
from pathlib import Path

import numpy as np
import pandas as pd
import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from src.services.ml_upload import (  # noqa: E402
    MIN_SPAN_DAYS,
    UploadError,
    parse_telemetry_file,
    score_telemetry,
)


def _csv_bytes(days: int, channels: dict[str, float]) -> bytes:
    idx = pd.date_range("2026-01-01", periods=days * 24, freq="h")
    df = pd.DataFrame({"ts": idx, **{c: v for c, v in channels.items()}})
    return df.to_csv(index=False).encode()


GOOD = {"F_out": 50.0, "Pump_intake_pressure_kgs": 45.0,
        "Motor_winding_temperature": 92.0, "Ia": 22.0, "CosF": 0.87, "Ped_load": 55.0}


def test_parse_ok() -> None:
    df = parse_telemetry_file(_csv_bytes(65, GOOD), "a.csv")
    assert isinstance(df.index, pd.DatetimeIndex)
    assert "F_out" in df.columns


def test_too_short_span_rejected() -> None:
    with pytest.raises(UploadError, match="минимум"):
        parse_telemetry_file(_csv_bytes(MIN_SPAN_DAYS - 10, GOOD), "a.csv")


def test_bad_extension_rejected() -> None:
    with pytest.raises(UploadError, match="CSV и XLSX"):
        parse_telemetry_file(b"x", "a.txt")


def test_oversize_rejected() -> None:
    """Лимит привязан к константе: двухмесячная выгрузка 40 каналов по минутам
    весит около 15 МБ, и прежний порог в 10 МБ отсекал нормальный файл."""
    from src.services.ml_upload import MAX_SIZE_MB

    assert MAX_SIZE_MB >= 15
    with pytest.raises(UploadError, match="МБ"):
        parse_telemetry_file(b"0" * ((MAX_SIZE_MB + 1) * 1024 * 1024), "a.csv")


def test_debit_from_file_uses_armits_columns() -> None:
    """Дебит берётся из подмешанных замеров АРМИТС (qg/dat_zam), а не из
    мёртвых каналов станции управления."""
    from src.services.ml_upload import _debit_from_file

    idx = pd.date_range("2025-01-01", periods=48, freq="1h")
    df = pd.DataFrame({"F_out": 50.0, "qg": [40.0] * 24 + [35.0] * 24,
                       "dat_zam": [pd.Timestamp("2025-01-01")] * 24
                                  + [pd.Timestamp("2025-01-02")] * 24}, index=idx)
    daily = _debit_from_file(df)
    assert daily is not None and len(daily) == 2
    assert daily["qg"].tolist() == [40.0, 35.0]
    assert daily["qn"].isna().all()          # нефти в файле нет — честный NaN
    assert _debit_from_file(pd.DataFrame({"F_out": [50.0]}, index=idx[:1])) is None


def test_no_time_column_rejected() -> None:
    df = pd.DataFrame({"foo": ["a", "b"], "bar": [1, 2]})
    with pytest.raises(UploadError, match="времени"):
        parse_telemetry_file(df.to_csv(index=False).encode(), "a.csv")


class _FakeModel:
    """Модель-заглушка: риск = клип(F_out_mean / 100)."""

    def predict_proba(self, X):
        p = np.clip(np.asarray(X["running_fraction"], dtype=float), 0, 1)
        return np.column_stack([1 - p, p])


def test_score_verdict_norm() -> None:
    from esp.pipeline.feature_matrix import MatrixConfig, build_feature_matrix

    df = parse_telemetry_file(_csv_bytes(65, GOOD), "a.csv")
    # feat_cols — реальные признаки, которые строятся из этих каналов
    probe = build_feature_matrix(df.head(500), well_id=0, oilfield="t",
                                 canonicalize=False, mcfg=MatrixConfig())
    numeric = [c for c in probe.columns
               if c not in ("well_id", "oilfield", "window_end")
               and probe[c].notna().any()][:8]
    assert "running_fraction" in numeric or numeric, numeric
    feat_cols = (["running_fraction"] if "running_fraction" not in numeric else []) + numeric
    ctx = {"model": _FakeModel(), "feat_cols": feat_cols, "calibrator": None}
    # running_fraction=1 (насос работает) -> заглушка даст p=1 на каждом окне -> alert
    res = score_telemetry(df, ctx)
    assert res["verdict"] == "alert"
    assert res["n_windows"] > 50
    assert 0 <= res["latest_risk"] <= 1
    assert len(res["timeline"]) == res["n_windows"]


def test_score_too_few_channels() -> None:
    idx = pd.date_range("2026-01-01", periods=65 * 24, freq="h")
    df = pd.DataFrame({"unknown_channel": 1.0}, index=idx)
    ctx = {"model": _FakeModel(), "feat_cols": ["a", "b", "c", "d", "e", "f"],
           "calibrator": None}
    with pytest.raises(UploadError):
        score_telemetry(df, ctx)
