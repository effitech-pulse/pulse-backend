"""Проверка загруженного файла телеметрии: будет ли отказ.

Формат входа (csv / xlsx, до 10 МБ, минимум 2 месяца данных):
  колонка времени ``ts`` (или ``timestamp``/``Дата``/первая колонка с датами)
  + каналы телеметрии. Имена каналов — канонические alias (``F_out``,
  ``Pump_intake_pressure_kgs``...) или русские displayname парсера
  («Давление на приеме ЭЦН, ат») — канонизация та же, что в боевом пайплайне
  (esp/naming.py), поэтому выгрузки IRZ подходят как есть.

Скоринг: окна 48 ч / шаг 12 ч (как в обучении) → модель stage1_binary_v1 →
калиброванный риск по каждому окну. Вердикт — те же ярусы, что в мониторинге:
  тревога:     >= K_hi окон с сырым скором > t_hi (precision ~0.80);
  наблюдение:  >= K окон с сырым скором > t (рабочая точка 0.69/0.87);
  норма:       иначе.
Кустовые/дебитные признаки для загруженного файла недоступны — заполняются
нулями (как NaN при обучении); точность чуть ниже боевой, о чём говорим в UI.
"""

from __future__ import annotations

import io
import logging
import sys
from pathlib import Path

import numpy as np
import pandas as pd

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from esp.naming import canonical_columns  # noqa: E402
from esp.pipeline.feature_matrix import MatrixConfig, build_feature_matrix  # noqa: E402

logger = logging.getLogger(__name__)

#: двухмесячная выгрузка 40 каналов по минутной сетке в xlsx — около 15 МБ;
#: прежний лимит в 10 МБ отсекал нормальный файл технолога
MAX_SIZE_MB = 50
#: жёсткий минимум истории. Правилу ярусов нужно 16 окон выше порога — это 8
#: суток, плюс запас на собственную базу скважины. Два месяца — рекомендация
#: в интерфейсе, а не отсечка: файл на 59 суток отбрасывался по границе.
MIN_SPAN_DAYS = 30

#: запасные пороги ярусов на СЫРОЙ шкале — если у модели нет меты калибровки.
#: Рабочие значения берутся из неё же (см. ``_tier_rule``): вшитые константы
#: пережили переобучение и разъехались с боевыми порогами.
FALLBACK_WATCH_T, FALLBACK_WATCH_K = 0.7, 5
FALLBACK_ALERT_T, FALLBACK_ALERT_K = 0.9, 4

_TIME_CANDIDATES = ("ts", "dt", "timestamp", "time", "datetime", "дата", "время")


class UploadError(ValueError):
    """Ошибка валидации загруженного файла (превращается в HTTP 422)."""


def parse_telemetry_file(content: bytes, filename: str) -> pd.DataFrame:
    """Байты файла → DataFrame с DatetimeIndex и каноническими колонками."""
    if len(content) > MAX_SIZE_MB * 1024 * 1024:
        raise UploadError(f"Файл больше {MAX_SIZE_MB} МБ")
    name = filename.lower()
    try:
        if name.endswith(".csv"):
            df = pd.read_csv(io.BytesIO(content))
        elif name.endswith((".xlsx", ".xls")):
            df = pd.read_excel(io.BytesIO(content))
        else:
            raise UploadError("Поддерживаются только форматы CSV и XLSX")
    except UploadError:
        raise
    except Exception as exc:  # noqa: BLE001
        raise UploadError(f"Не удалось прочитать файл: {exc}") from exc
    if df.empty:
        raise UploadError("Файл пуст")

    # колонка времени: по имени или первая колонка, похожая на даты
    time_col = next((c for c in df.columns if str(c).strip().lower() in _TIME_CANDIDATES), None)
    if time_col is None:
        time_col = df.columns[0]
    ts = pd.to_datetime(df[time_col], errors="coerce")
    if ts.isna().mean() > 0.05:
        raise UploadError(
            f"Колонка времени «{time_col}» не распознана: нужна колонка ts/timestamp "
            "с датой-временем (например 2026-01-15 10:30:00)")
    df = df.drop(columns=[time_col])
    df.index = ts
    df = df[~df.index.isna()].sort_index()

    df = canonical_columns(df)
    span = (df.index.max() - df.index.min()).days
    if span < MIN_SPAN_DAYS:
        raise UploadError(
            f"В файле {span} суток данных — нужно минимум {MIN_SPAN_DAYS}")
    return df


def _tier_rule(ctx: dict) -> tuple[tuple[float, int], tuple[float, int]]:
    """Правило ярусов из меты калибровки: ``((порог, K) наблюдение, … тревога)``.

    Разовая проверка файла обязана судить по тому же правилу, что и прогон по
    фонду, иначе одна и та же скважина получает разные вердикты в двух местах
    интерфейса. Пороги здесь на СЫРОЙ шкале — калибратор монотонен, поэтому
    сравнение эквивалентно сравнению калиброванных значений.
    """
    cal = (ctx.get("calibration") or {})
    high = cal.get("high_tier") or {}
    per_well = cal.get("per_well") or {}
    watch_t = float(cal.get("raw_threshold") or FALLBACK_WATCH_T)
    watch_k = int(per_well.get("k_windows") or FALLBACK_WATCH_K)
    alert_t = float(high.get("raw_threshold") or FALLBACK_ALERT_T)
    alert_k = int(high.get("k_windows") or FALLBACK_ALERT_K)
    return (watch_t, watch_k), (alert_t, alert_k)


def _debit_from_file(df: pd.DataFrame) -> pd.DataFrame | None:
    """Суточный дебит из самого файла, если выгрузка АРМИТС в него подмешана.

    В выгрузках технологов рядом с телеметрией часто идут колонки замера
    дебита: ``qg`` (факт), ``qg_tex`` (норма), ``qn`` (нефть) и дата замера
    ``dat_zam``. Каналы дебита самой станции управления (``Flowrate for
    current day``) в фонде мертвы — нули на 99% скважин, — поэтому берём
    именно замеры. Нет ни одного — дебит-признаки останутся пустыми.
    """
    if "qg" not in df.columns:
        return None
    day = (pd.to_datetime(df["dat_zam"], errors="coerce") if "dat_zam" in df.columns
           else df.index.normalize())
    daily = pd.DataFrame({"qg": pd.to_numeric(df["qg"], errors="coerce").to_numpy(),
                          "date": pd.to_datetime(day).dt.normalize().to_numpy()})
    for col in ("qg_tex", "qn"):
        daily[col] = (pd.to_numeric(df[col], errors="coerce").to_numpy()
                      if col in df.columns else np.nan)
    daily = daily.dropna(subset=["date"]).groupby("date").last().sort_index()
    return daily if daily["qg"].notna().any() else None


def score_telemetry(df: pd.DataFrame, ctx: dict) -> dict:
    """Канонизированная телеметрия → риск по окнам + вердикт + объяснение.

    Модель обучена на 68 признаках, а из одного файла вычислимы не все: нет
    соседей по кусту, нет статистики месторождения. Раньше недостающее
    заполнялось нулями, и модель читала «медианный дебит куста = 0» как
    аномалию — вердикт зависел от того, чего в файле НЕТ, а не от того, что
    в нём есть. Теперь: дебит и отклонение от собственного режима считаются
    из файла, остальное заполняется медианами обучающей выборки, а число
    недоступных признаков возвращается наружу — технолог видит, насколько
    полной была оценка.
    """
    matrix = build_feature_matrix(
        df, well_id=0, oilfield="uploaded", canonicalize=False, mcfg=MatrixConfig(),
        debit=_debit_from_file(df))
    if matrix.empty:
        raise UploadError(
            "Не удалось построить ни одного окна признаков — проверьте, что в файле "
            "есть каналы телеметрии (F_out, давление на приёме, токи и т.д.)")

    feat_cols: list[str] = ctx["feat_cols"]
    known = [c for c in feat_cols if c in matrix.columns and matrix[c].notna().any()]
    if len(known) < 5:
        raise UploadError(
            "В файле слишком мало известных каналов телеметрии "
            f"(распознано признаков: {len(known)}). Ожидаются каналы УЭЦН: F_out, "
            "Pump_intake_pressure_kgs, Motor_winding_temperature, Ia/Ib/Ic, CosF...")

    # отклонение от собственного режима вычислимо из истории самого файла
    from esp.features.cluster import add_own_baseline_deviation
    matrix = add_own_baseline_deviation(matrix, ("running_fraction", "n_pump_transitions"))

    for c in feat_cols:
        if c not in matrix.columns:
            matrix[c] = np.nan
    X = matrix[feat_cols].astype(float)
    unavailable = [c for c in feat_cols if X[c].isna().all()]
    medians = ((ctx.get("drift_profile") or {}).get("medians") or {})
    for c in unavailable:
        X[c] = medians.get(c, 0.0)
    X = X.fillna(0.0)

    model = ctx["model"]
    proba = model.predict_proba(X)
    raw = proba[:, 1] if proba.ndim == 2 else proba
    calibrator = ctx.get("calibrator")
    risk = calibrator.predict(raw) if calibrator is not None else raw

    (watch_t, watch_k), (alert_t, alert_k) = _tier_rule(ctx)
    n_watch = int((raw > watch_t).sum())
    n_alert = int((raw > alert_t).sum())

    # Достаточность данных. Правило тревоги требует alert_k окон выше порога —
    # если установка работала меньше, чем в таком числе окон, тревога физически
    # недостижима, и «норма» была бы не оценкой, а её отсутствием. Так бывает,
    # когда выгрузка на две трети состоит из нулей: скважина стояла или не
    # передавала, а экспорт записал это нулями по всем каналам.
    running = pd.to_numeric(matrix.get("running_fraction"), errors="coerce").fillna(0.0)
    n_running = int((running >= 0.5).sum())
    if n_alert >= alert_k:
        verdict = "alert"
    elif n_watch >= watch_k:
        verdict = "watch"
    elif n_running < alert_k:
        verdict = "insufficient"
    else:
        verdict = "norm"

    # объяснение по самому рискованному окну
    from src.services.ml_scoring import shap_top_contributions
    worst = int(np.argmax(raw))
    contribs = shap_top_contributions(model, X.iloc[worst], feat_cols, top_n=6)

    window_end = pd.to_datetime(matrix["window_end"])
    return {
        "verdict": verdict,
        "n_windows": len(matrix),
        "span_days": int((df.index.max() - df.index.min()).days),
        "channels_used": len(known),
        "features_total": len(feat_cols),
        "windows_running": n_running,
        "features_unavailable": len(unavailable),
        "windows_above_watch": n_watch,
        "windows_above_alert": n_alert,
        "latest_risk": round(float(risk[-1]), 4),
        "peak_risk": round(float(risk.max()), 4),
        "peak_window_end": window_end.iloc[worst].isoformat(),
        "timeline": [
            {"window_end": we.isoformat(), "risk": round(float(r), 4)}
            for we, r in zip(window_end, risk)
        ],
        "contributions": contribs,
    }
