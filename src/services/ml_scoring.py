"""Скоринг скважин: последнее доступное окно каждой скважины -> риск (Стадия 1).

Переиспользует всю уже протестированную ML-инфраструктуру проекта (esp/) — не
дублирует логику. Кустовой/цеховой контекст (esp.features.cluster) считается
здесь так же, как в scripts/eval_per_well.py — после конкатенации окон всех
скважин, не пер-скважинно (иначе относительный дебит/давление отказов соседей
не с чем сравнивать).

Скоринг батчевый (не построчный real-time): пересчитывается по запросу
(``score_all_wells``) и держится в памяти процесса — прогонять кустовой
контекст на каждый HTTP-запрос было бы избыточно дорого.
"""

from __future__ import annotations

import logging
import sys
from pathlib import Path

import numpy as np
import pandas as pd

from src.core.ml_paths import ML_DATA_ROOT
PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from esp.pipeline.cascade import stage0_rules  # noqa: E402
from esp.pipeline.context import add_context_features  # noqa: E402
from esp.labeling.manifest import load_manifest_labels  # noqa: E402
from esp.models.gbm import predict_proba  # noqa: E402
from esp.models.registry import load_model  # noqa: E402
from esp.pipeline.cache import read_well_cache  # noqa: E402

logger = logging.getLogger(__name__)


def latest_window_per_well(cache_dir: str) -> pd.DataFrame:
    """Одна строка на скважину — её самое свежее (по ``window_end``) окно из кэша.

    Возвращает ``(последние окна, вся история)``: история нужна для признаков,
    которые сравнивают окно с прошлым самой скважины.
    """
    frames = read_well_cache(cache_dir)
    if not frames:
        return pd.DataFrame(), pd.DataFrame()
    ds = pd.concat(frames, ignore_index=True)
    idx = ds.groupby("well_id")["window_end"].idxmax()
    return ds.loc[idx].reset_index(drop=True), ds


def score_all_wells(
    cache_dir: str = str(ML_DATA_ROOT / "cache" / "features_v3"),
    manifest_path: str = str(ML_DATA_ROOT / "manifest" / "grant_manifest_1000.xlsx"),
    model_name: str = "stage1_binary_v1",
    models_dir: str = str(ML_DATA_ROOT / "models"),
) -> pd.DataFrame:
    """Риск (P(отказ)) по каждой скважине на её последнем доступном окне.

    Тонкая обёртка над :func:`score_all_wells_full` (тот же пайплайн + контекст
    для SHAP). Возвращает только таблицу скоров, отсортированную по риску убыв.
    Пустой DataFrame, если кэш пуст (например, ещё пересобирается).
    """
    return score_all_wells_full(cache_dir, manifest_path, model_name, models_dir)["scores"]


def score_all_wells_full(
    cache_dir: str = str(ML_DATA_ROOT / "cache" / "features_v3"),
    manifest_path: str = str(ML_DATA_ROOT / "manifest" / "grant_manifest_1000.xlsx"),
    model_name: str = "stage1_binary_v1",
    models_dir: str = str(ML_DATA_ROOT / "models"),
) -> dict:
    """Полный скоринг: таблица скоров + контекст для объяснений (SHAP).

    Риск считается по ВСЕМ окнам каждой скважины, а не только по последнему —
    по двум причинам. Во-первых, контекстные признаки (уровень месторождения,
    отклонение от собственного режима) при обучении считались по всему фонду и
    всей истории; посчитанные по одному срезу, они дают другие числа, и модель
    получает не то, на чём училась. Во-вторых, ярус скважины определяется
    правилом «не менее K сработавших окон» — тем самым, на котором измерены
    заявленные полнота и точность. Полный проход стоит около полутора секунд
    на 207 тысяч окон, то есть дешевле, чем расхождение с отчётностью.

    Возвращает ``{scores, X, feat_cols, model, ...}``; ``X`` индексирован
    ``well_id`` — признаки последнего окна скважины (для объяснения алерта).
    """
    man = load_manifest_labels(manifest_path)
    latest, history = latest_window_per_well(cache_dir)
    if latest.empty:
        return {"scores": latest, "X": pd.DataFrame(), "feat_cols": [], "model": None}

    from utils.loaders._pg import pg_connect

    # Тот же код, что в сборщике обучающего датасета: кустовой контекст,
    # отклонение от собственного режима скважины и нормировки на месторождение.
    # Пока эти шаги были записаны здесь отдельно, модель, обученная на полном
    # наборе, получала на инференсе нули вместо двенадцати признаков.
    scored = add_context_features(history, man, pg_conn=pg_connect())

    trained = load_model(model_name, models_dir=models_dir)
    feat_cols = list(getattr(trained["model"], "feature_names_", trained["classes"]))
    for c in feat_cols:
        if c not in scored.columns:
            scored[c] = float("nan")
    proba = predict_proba(trained, scored[feat_cols].astype(float).fillna(0.0))
    risk_col = "prob_1" if "prob_1" in proba.columns else proba.columns[-1]
    risk_raw = proba[risk_col].to_numpy()

    # Калибратор (isotonic, scripts/calibrate_model.py): переводит сырой скор
    # в честную вероятность. Порог тревоги — тоже с калиброванной шкалы.
    calibrator = trained.get("calibrator")
    calibration = (trained.get("meta") or {}).get("calibration") or {}
    high_tier = calibration.get("high_tier") or {}
    if calibrator is not None:
        scored["risk"] = calibrator.predict(risk_raw)
        alert_threshold = float(calibration.get("calibrated_threshold", 0.7))
        high_threshold = float(high_tier.get("calibrated_threshold") or 1.0)
    else:
        scored["risk"] = risk_raw
        alert_threshold = 0.7
        high_threshold = 1.0
    k_watch = int((calibration.get("per_well") or {}).get("k_windows") or 1)
    k_alert = int(high_tier.get("k_windows") or k_watch)

    # ярус по правилу «K сработавших окон за историю наблюдения»
    by_well = scored.groupby("well_id")["risk"]
    fired_watch = by_well.apply(lambda s: int((s >= alert_threshold).sum()))
    fired_alert = by_well.apply(lambda s: int((s >= high_threshold).sum()))
    windows_total = by_well.size()

    # последнее окно — то, что показывается как текущий риск и объясняется SHAP
    latest = scored.loc[scored.groupby("well_id")["window_end"].idxmax()].reset_index(drop=True)
    X = latest[feat_cols].astype(float).fillna(0.0)
    latest["windows_fired"] = latest["well_id"].map(fired_watch).fillna(0).astype(int)
    latest["windows_fired_high"] = latest["well_id"].map(fired_alert).fillna(0).astype(int)
    latest["windows_total"] = latest["well_id"].map(windows_total).fillna(0).astype(int)
    latest["tier"] = np.where(
        latest["windows_fired_high"] >= k_alert, "alert",
        np.where(latest["windows_fired"] >= k_watch, "watch", None))

    # Стадия 0 на последнем окне: достоверность текущего риска и сработавшие
    # правила. Ярус при этом НЕ гасится: потеря связи обесценивает свежий
    # замер, но не отменяет уже накопленные свидетельства по истории —
    # замолчавшая скважина не становится исправной.
    stage0 = [stage0_rules(latest.iloc[i]) for i in range(len(latest))]
    latest["data_ok"] = [not bad for bad, _ in stage0]
    latest["rule_findings"] = ["; ".join(r) for _, r in stage0]

    latest["oilfield"] = latest["well_id"].map(lambda w: man.get(w, {}).get("oilfield"))
    latest["unitek_id"] = latest["well_id"].map(lambda w: man.get(w, {}).get("unitek_id"))
    latest["true_failure_type"] = latest["well_id"].map(
        lambda w: man.get(w, {}).get("failure_type"))
    latest["true_failure_at"] = latest["well_id"].map(
        lambda w: man.get(w, {}).get("failure_at"))

    scores = latest[["well_id", "unitek_id", "oilfield", "window_end", "risk",
                     "tier", "windows_fired", "windows_fired_high", "windows_total",
                     "data_ok", "rule_findings",
                     "true_failure_type", "true_failure_at"]].copy()
    scores = scores.sort_values(["windows_fired", "risk"], ascending=False).reset_index(drop=True)
    X_indexed = X.copy()
    X_indexed.index = latest["well_id"].to_numpy()
    # История риска по окнам — для карточки скважины: технолог должен видеть,
    # КАК риск набирался, а не только итоговое число. Это же и есть основание
    # яруса: ярус ставится по числу сработавших окон.
    history_risk = scored[["well_id", "window_end", "risk"]].sort_values(
        ["well_id", "window_end"]).reset_index(drop=True)

    return {"scores": scores, "X": X_indexed, "feat_cols": feat_cols,
            "history_risk": history_risk,
            "model": trained["model"], "alert_threshold": alert_threshold,
            "high_threshold": high_threshold, "calibrator": calibrator,
            "k_watch": k_watch, "k_alert": k_alert,
            # мета калибровки нужна разовой проверке файла: она обязана судить
            # по тому же правилу ярусов, что и прогон по фонду
            "calibration": calibration,
            "calibrated": calibrator is not None,
            "drift_profile": trained.get("drift_profile")}


def shap_top_contributions(model, x_row: pd.Series, feat_cols: list[str],
                           top_n: int = 6) -> list[dict]:
    """Топ-``top_n`` признаков по |SHAP| для одной скважины (нативный CatBoost TreeSHAP).

    Возвращает список ``{feature, value, shap}`` по убыванию |shap|: положительный
    shap толкает риск вверх, отрицательный — вниз. Fail-soft: не-CatBoost модель
    (sklearn/prior-фолбэк без get_feature_importance) → пустой список.
    """
    try:
        from catboost import Pool

        pool = Pool(x_row.to_frame().T[feat_cols])
        shap = model.get_feature_importance(type="ShapValues", data=pool)[0]
    except Exception as exc:  # noqa: BLE001 — объяснение опционально
        logger.warning("SHAP недоступен: %s", exc)
        return []
    # MultiClass-loss даёт (n_classes, n_features+1) — берём вклад класса «отказ» (1)
    if getattr(shap, "ndim", 1) == 2:
        shap = shap[1] if shap.shape[0] > 1 else shap[0]
    contrib = shap[:-1]  # последний элемент — базовое значение (bias)
    order = abs(contrib).argsort()[::-1][:top_n]
    return [{"feature": feat_cols[i], "value": float(x_row[feat_cols[i]]),
             "shap": float(contrib[i])} for i in order]
