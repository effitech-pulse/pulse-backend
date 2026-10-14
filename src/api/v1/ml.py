"""ML-контур (Стадия 1): риск отказа по скважинам + метрики моделей.

Мост между операционным приложением и исследовательским ML-ядром (esp/):
скоринг сохранённой моделью CatBoost по последнему окну признаков каждой
скважины (кэш data/cache/features + кустовой контекст). Скоринг батчевый,
в памяти процесса; пересчёт — POST /ml/wells/refresh.
"""
from __future__ import annotations

import logging
import sys
from pathlib import Path

import pandas as pd
from fastapi import APIRouter, Depends, File, HTTPException, UploadFile
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.orm import Session

from src.core.ml_paths import ML_DATA_ROOT
PROJECT_ROOT = Path(__file__).resolve().parents[3]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from esp.labeling.weak.signatures import FailureClass  # noqa: E402
from esp.models.registry import list_models  # noqa: E402

from src.core.database import get_db  # noqa: E402
from src.models.ml_run import MlAlert, MlRun  # noqa: E402
from src.models.well import Well  # noqa: E402
from src.services.ml_scoring import shap_top_contributions  # noqa: E402
from src.services.ml_store import (  # noqa: E402
    get_alert_threshold,
    get_high_threshold,
    get_context,
    get_scores,
    is_running,
    model_health,
    start_background_run,
)

logger = logging.getLogger(__name__)
router = APIRouter(prefix="/ml", tags=["ml"])

TYPE_NAMES = {int(f): f.name for f in FailureClass}


def _iso(ts) -> str | None:
    return ts.isoformat() if pd.notna(ts) else None


#: русские подписи признаков для объяснений (остальные — техническим именем)
FEATURE_RU = {
    "debit_norm_ratio": "Дебит: доля от нормы",
    "debit_qg_slope": "Дебит: тренд, %/сут",
    "debit_qg_last": "Дебит жидкости (факт)",
    "debit_qg_tex_last": "Дебит: технологическая норма",
    "debit_qg_mean": "Дебит: средний за 14 сут",
    "debit_qn_last": "Дебит нефти",
    "debit_watercut": "Обводнённость",
    "debit_downtime_frac": "Дебит: доля простоя",
    "running_fraction": "Доля времени работы",
    "running_fraction_vs_own_base": "Работа: отклонение от своего режима",
    "n_pump_transitions": "Переключений насоса в окне",
    "pump_cycle_cv": "Нерегулярность цикла вкл/выкл",
    "current_fluctuation": "Флуктуации тока",
    "specific_energy": "Удельное энергопотребление",
    "zsp_total_count": "Срабатываний защиты (ЗСП)",
    "t_limit_fraction": "Режим ограничения по температуре",
    "I_in_band_fraction": "Ток в полосе ХХ–номинал",
    "I_le_xx_fraction": "Ток ниже холостого хода",
    "I_gt_nom_fraction": "Ток выше номинала",
    "CosF_below_fraction": "cos φ ниже порога",
    "insulation_alarm": "Тревога изоляции (Стадия 0)",
    "vfd_alarm": "Тревога ЧРЭП/сети (Стадия 0)",
    "frozen_data": "Потеря связи (Стадия 0)",
    "steady_fraction": "Доля времени без изменений показаний",
    "active_power_mean": "Активная мощность (среднее)",
    "specific_energy_debit": "Удельное энергопотребление (по дебиту)",
    "P_out_active_slope": "Активная мощность: тренд",
    "P_out_full_slope": "Полная мощность: тренд",
    "U_out_fc_slope": "Напряжение на выходе СУ: тренд",
    "Ud_slope": "Напряжение звена постоянного тока: тренд",
    "Motor_oil_temperature_slope": "Температура масла ПЭД: тренд",
    "T_rad_fc_slope": "Температура радиатора СУ: тренд",
    "Motor_oil_temperature_const_f_slope": "Температура масла ПЭД при const f",
    "T_rad_fc_const_f_slope": "Температура радиатора СУ при const f",
    "Pump_intake_pressure_kgs_slope": "Давление на приёме: тренд",
    "Annulus_pressure_slope": "Затрубное давление: тренд",
    "Motor_winding_temperature_slope": "Температура обмотки: тренд",
    "Pump_intake_temperature_slope": "Температура приёма: тренд",
    "Ped_load_slope": "Загрузка ПЭД: тренд",
    "Pump_discharge_flow_slope": "Расход на выкиде: тренд",
    "CosF_slope": "cos φ: тренд",
    "cluster_debit_median": "Куст: медианный дебит группы",
    "debit_vs_cluster_ratio": "Дебит относительно куста",
    "cluster_debit_rank_pct": "Ранг дебита в кусте",
    "cluster_neighbors_failed": "Отказы соседей (30 сут)",
    "cluster_neighbors_failed_frac": "Доля отказавших соседей (30 сут)",
    "I_disbalance_slope": "Дисбаланс токов: тренд",
    "U_disbalance_slope": "Дисбаланс напряжений: тренд",
    "R_izol_slope": "Сопротивление изоляции: тренд",
    "Vibration_X_slope": "Вибрация по оси X: тренд",
    "Vibration_Y_slope": "Вибрация по оси Y: тренд",
    "Vibration_Z_slope": "Вибрация по оси Z: тренд",
    "Vibration_XYZ_slope": "Вибрация суммарная: тренд",
    "stat_Number_stop_underload_count": "Остановки по недогрузу за окно",
    "stat_Number_stop_overload_count": "Остановки по перегрузу за окно",
    "stat_Number_stop_other_count": "Прочие защитные остановки за окно",
}

#: производные признаки собираются из подписи базового: перечислять все
#: сочетания вручную — значит однажды показать технологу «steady_fraction_field_z»
#: вместо человеческого имени (так и было до 11.09.2026)
FEATURE_SUFFIX_RU = {
    "_const_f_slope": "{}: тренд при постоянной частоте",
    "_field_z": "{}: к уровню месторождения",
    "_vs_own_base": "{}: к своему режиму",
}


def feature_label(name: str) -> str:
    """Человеческая подпись признака для объяснения алерта.

    Сначала точное совпадение, затем разбор производных суффиксов, и только
    потом — техническое имя как есть.
    """
    if name in FEATURE_RU:
        return FEATURE_RU[name]
    for suffix, tpl in FEATURE_SUFFIX_RU.items():
        if name.endswith(suffix):
            base = name[: -len(suffix)]
            label = FEATURE_RU.get(base) or FEATURE_RU.get(f"{base}_slope")
            if label:
                # у базовой подписи мог быть свой хвост («: тренд») — срезаем
                return tpl.format(label.split(":")[0])
    return name


def _well_dict(r, well_map: dict | None = None, alert_map: dict | None = None) -> dict:
    threshold = get_alert_threshold()
    high = get_high_threshold()
    # ярус приходит из скоринга: он считается по правилу «K сработавших окон»,
    # тому же, на котором измерены заявленные полнота и точность. Порог риска
    # последнего окна для яруса недостаточен — одиночное окно тревогой не
    # считается (эксп. #7)
    tier = getattr(r, "tier", None)
    tier = None if tier is None or (isinstance(tier, float) and pd.isna(tier)) else str(tier)
    ftype = int(r.true_failure_type) if pd.notna(r.true_failure_type) else None
    unitek = int(r.unitek_id) if pd.notna(r.unitek_id) else None
    alert_row = (alert_map or {}).get(unitek)
    return {
        "well_id": int(r.well_id),
        "unitek_id": unitek,
        "app_well_id": (well_map or {}).get(unitek),
        "alert_id": alert_row[0] if alert_row else None,
        "alert_status": alert_row[1] if alert_row else None,
        "oilfield": r.oilfield,
        "risk": round(float(r.risk), 4),
        "alert": tier is not None,
        "tier": tier,
        "windows_fired": int(getattr(r, "windows_fired", 0) or 0),
        "windows_total": int(getattr(r, "windows_total", 0) or 0),
        # Стадия 0: достоверно ли последнее окно и какие правила сработали.
        # Ярус от этого не зависит — потеря связи обесценивает свежий замер,
        # но не отменяет накопленные свидетельства по истории
        "data_ok": bool(getattr(r, "data_ok", True)),
        "rule_findings": [x for x in str(getattr(r, "rule_findings", "") or "").split("; ") if x],
        "window_end": _iso(r.window_end),
        "true_failure_type": ftype,
        "true_failure_type_name": TYPE_NAMES.get(ftype) if ftype is not None else None,
        "true_failure_at": _iso(r.true_failure_at),
    }


def _well_id_map(db: Session) -> dict:
    """unitek_id -> id скважины в таблице well приложения."""
    return dict(db.execute(select(Well.unitek_id, Well.id)).all())


def _latest_alert_map(db: Session) -> dict:
    """unitek_id -> (alert_id, status) из последнего завершённого прогона."""
    run_id = db.scalar(
        select(MlRun.id).where(MlRun.status == "done").order_by(MlRun.id.desc()).limit(1)
    )
    if run_id is None:
        return {}
    rows = db.execute(
        select(MlAlert.unitek_id, MlAlert.id, MlAlert.status).where(MlAlert.run_id == run_id)
    )
    return {u: (a, st) for u, a, st in rows if u is not None}


@router.get("/wells")
def ml_wells(
    min_risk: float | None = None,
    alert_only: bool = False,
    db: Session = Depends(get_db),
) -> dict:
    df = get_scores()
    if df.empty:
        raise HTTPException(503, "Скоринг недоступен: кэш признаков пуст "
                                 "(пересборка make train-full ещё идёт?)")
    if min_risk is not None:
        df = df[df["risk"] >= min_risk]
    if alert_only:
        # «тревожные» — те, кому ярус присвоен правилом K окон, а не разовым
        # превышением порога последним окном
        df = df[df["tier"].notna()] if "tier" in df.columns else \
            df[df["risk"] >= get_alert_threshold()]
    well_map = _well_id_map(db)
    alert_map = _latest_alert_map(db)
    return {
        "count": len(df),
        "alert_threshold": get_alert_threshold(),
        "high_threshold": get_high_threshold(),
        "running": is_running(),
        "wells": [_well_dict(r, well_map, alert_map) for r in df.itertuples()],
    }


@router.post("/wells/refresh")
def ml_refresh() -> dict:
    """Запускает фоновый прогон модели по фонду. 409 — если прогон уже идёт."""
    if not start_background_run("manual"):
        raise HTTPException(409, "Прогон уже выполняется")
    return {"started": True}


def _run_dict(r: MlRun) -> dict:
    return {
        "id": r.id,
        "started_at": r.started_at.isoformat(),
        "finished_at": r.finished_at.isoformat() if r.finished_at else None,
        "status": r.status,
        "trigger": r.trigger,
        "wells_scored": r.wells_scored,
        "alerts_count": r.alerts_count,
        "error": r.error,
        "drift_verdict": r.drift_verdict,
        "drift_psi_max": r.drift_psi_max,
        "drift_top": r.drift_top,
    }


@router.get("/runs")
def ml_runs(limit: int = 20, db: Session = Depends(get_db)) -> dict:
    """История прогонов (новые сверху) + флаг текущего прогона."""
    rows = db.scalars(select(MlRun).order_by(MlRun.id.desc()).limit(limit)).all()
    return {"running": is_running(), "runs": [_run_dict(r) for r in rows]}


class AlertStatusUpdate(BaseModel):
    status: str  # new | acknowledged | false_positive


@router.patch("/alerts/{alert_id}")
def ml_alert_status(
    alert_id: int, payload: AlertStatusUpdate, db: Session = Depends(get_db)
) -> dict:
    """Обратная связь технолога по тревоге: принято в работу / ложная."""
    if payload.status not in ("new", "acknowledged", "false_positive"):
        raise HTTPException(422, f"Недопустимый статус: {payload.status}")
    alert = db.get(MlAlert, alert_id)
    if alert is None:
        raise HTTPException(404, f"Тревога {alert_id} не найдена")
    alert.status = payload.status
    db.commit()
    return {"id": alert.id, "status": alert.status}


@router.get("/wells/by-unitek/{unitek_id}")
def ml_well_by_unitek(unitek_id: int, db: Session = Depends(get_db)) -> dict:
    """Риск скважины по её Unitek ID (связка с таблицей well приложения)."""
    df = get_scores()
    if df.empty:
        raise HTTPException(503, "Скоринг недоступен: кэш признаков пуст")
    row = df[df["unitek_id"] == unitek_id]
    if row.empty:
        raise HTTPException(404, f"Скважина unitek_id={unitek_id} не найдена в скоринге")
    return _well_dict(row.iloc[0], _well_id_map(db), _latest_alert_map(db))


@router.get("/wells/{well_id}/explain")
def ml_explain(well_id: int, top_n: int = 6) -> dict:
    """SHAP-объяснение риска: какие признаки и куда толкают прогноз этой скважины."""
    ctx = get_context()
    X = ctx["X"]
    if X.empty or well_id not in X.index:
        raise HTTPException(404, f"Скважина {well_id} не найдена в скоринге")
    contribs = shap_top_contributions(
        ctx["model"], X.loc[well_id], ctx["feat_cols"], top_n=top_n)
    if not contribs:
        raise HTTPException(503, "Объяснение недоступно для текущего движка модели")
    return {
        "well_id": well_id,
        "contributions": [
            {**c, "label": feature_label(c["feature"])} for c in contribs
        ],
    }


@router.get("/wells/{well_id}/history")
def ml_well_history(well_id: int, limit: int = 400) -> dict:
    """Риск по всем окнам наблюдения скважины — как он набирался.

    Это основание яруса в явном виде: ярус ставится по числу окон, где риск
    превысил порог, и здесь видно, какие именно это окна. Итоговое число без
    такой истории технологу не проверить.
    """
    ctx = get_context()
    hist = ctx.get("history_risk")
    if hist is None or hist.empty:
        raise HTTPException(503, "История риска недоступна: скоринг ещё не выполнялся")
    rows = hist[hist["well_id"] == well_id]
    if rows.empty:
        raise HTTPException(404, f"Скважина {well_id} не найдена в скоринге")
    rows = rows.tail(limit)
    threshold = get_alert_threshold()
    return {
        "well_id": well_id,
        "threshold": threshold,
        "high_threshold": get_high_threshold(),
        "windows_total": int(len(hist[hist["well_id"] == well_id])),
        "windows_fired": int((rows["risk"] >= threshold).sum()),
        "points": [
            {"window_end": _iso(r.window_end), "risk": round(float(r.risk), 4),
             "fired": bool(r.risk >= threshold)}
            for r in rows.itertuples()
        ],
    }


@router.get("/health")
def ml_health() -> dict:
    """Состояние боевой модели: дрейф, обратная связь, нужно ли переобучение."""
    return model_health()


@router.get("/metrics")
def ml_metrics() -> dict:
    models = list_models(models_dir=str(ML_DATA_ROOT / "models"))
    return {"models": models}


@router.post("/upload")
async def ml_upload(file: UploadFile = File(...)) -> dict:
    """Проверка файла телеметрии (csv/xlsx, ≤50 МБ, ≥2 мес): будет ли отказ."""
    from src.services.ml_upload import UploadError, parse_telemetry_file, score_telemetry

    content = await file.read()
    try:
        df = parse_telemetry_file(content, file.filename or "upload.csv")
        result = score_telemetry(df, get_context())
    except UploadError as exc:
        raise HTTPException(422, str(exc)) from exc
    result["contributions"] = [
        {**c, "label": feature_label(c["feature"])}
        for c in result["contributions"]
    ]
    result["filename"] = file.filename
    return result
