"""Оркестратор ML-скоринга: кэш контекста, фоновые прогоны, история в БД.

Контекст (скоры + матрица признаков + модель) живёт в памяти процесса —
из него отдаются список рисков и SHAP-объяснения. Явный прогон
(кнопка / расписание) выполняется в фоновом потоке и фиксируется в БД:
строка ``ml_run`` + тревоги ``ml_alert`` (риск >= порога). Статусы тревог
(принято в работу / ложная) переносятся с предыдущего прогона по unitek_id.
"""

from __future__ import annotations

import logging
import threading
from datetime import datetime

import pandas as pd
from sqlalchemy import select

from pathlib import Path

from src.core.database import SessionLocal
from src.models.ml_run import MlAlert, MlRun
from src.models.well import Well
from esp.monitoring import drift_report
from src.services.ml_scoring import score_all_wells_full

logger = logging.getLogger(__name__)

#: порог тревоги по умолчанию (сырая шкала); при наличии калибратора
#: берётся calibrated_threshold из меты модели (см. get_alert_threshold)
DEFAULT_ALERT_THRESHOLD = 0.7

from src.core.ml_paths import ML_DATA_ROOT
PROJECT_ROOT = Path(__file__).resolve().parents[2]

_cache: dict | None = None
_lock = threading.Lock()
_running = threading.Event()


def get_context(force_refresh: bool = False) -> dict:
    """Полный контекст скоринга: ``{scores, X, feat_cols, model}``.

    Ленивый первый расчёт (без записи в историю — это не «прогон»,
    а прогрев кэша для GET-запросов).
    """
    global _cache
    if _cache is None or force_refresh:
        logger.info("пересчёт скоринга скважин...")
        _cache = score_all_wells_full()
        logger.info("скоринг готов: %d скважин", len(_cache["scores"]))
    return _cache


def get_scores(force_refresh: bool = False) -> pd.DataFrame:
    return get_context(force_refresh)["scores"]


def get_alert_threshold() -> float:
    """Актуальный порог тревоги (калиброванный, если модель откалибрована)."""
    if _cache is not None:
        return float(_cache.get("alert_threshold", DEFAULT_ALERT_THRESHOLD))
    return DEFAULT_ALERT_THRESHOLD


def get_high_threshold() -> float:
    """Порог верхнего яруса «тревога» (precision ~0.8); 1.0 — ярус недоступен."""
    if _cache is not None:
        return float(_cache.get("high_threshold", 1.0))
    return 1.0


def is_running() -> bool:
    return _running.is_set()


def start_background_run(trigger: str = "manual") -> bool:
    """Запускает прогон в фоновом потоке. False — если прогон уже идёт.

    Флаг ``running`` взводится здесь, до старта потока, — чтобы клиент,
    опросивший статус сразу после запуска, уже видел идущий прогон.
    """
    with _lock:
        if _running.is_set():
            return False
        _running.set()
    thread = threading.Thread(
        target=_run_and_persist, args=(trigger,), name=f"ml-run-{trigger}", daemon=True
    )
    thread.start()
    return True


def _run_and_persist(trigger: str) -> None:
    db = SessionLocal()
    run = MlRun(status="running", trigger=trigger, started_at=datetime.now())
    db.add(run)
    db.commit()
    db.refresh(run)
    try:
        ctx = get_context(force_refresh=True)
        scores = ctx["scores"]
        alerts = _persist_alerts(db, run.id, scores)
        run.status = "done"
        run.finished_at = datetime.now()
        run.wells_scored = len(scores)
        run.alerts_count = alerts
        _attach_drift(run, ctx)
        db.commit()
        logger.info("прогон #%d (%s): %d скважин, %d тревог",
                    run.id, trigger, len(scores), alerts)
    except Exception as exc:  # noqa: BLE001 — fail-soft: фиксируем ошибку в истории
        logger.exception("прогон #%d завершился ошибкой", run.id)
        db.rollback()
        run.status = "error"
        run.finished_at = datetime.now()
        run.error = str(exc)[:2000]
        db.commit()
    finally:
        db.close()
        _running.clear()


def _attach_drift(run: MlRun, ctx: dict) -> None:
    """Считает дрейф текущих данных относительно обучающей выборки.

    Fail-soft: если профиль не построен (модель обучена до появления детектора)
    или расчёт не удался, прогон завершается штатно, а вердикт остаётся пустым —
    мониторинг не должен ломать скоринг.
    """
    profile = ctx.get("drift_profile")
    X = ctx.get("X")
    if not profile or X is None or X.empty:
        logger.info("дрейф не рассчитан: нет эталонного профиля или данных")
        return
    try:
        rep = drift_report(profile, X)
    except Exception:  # noqa: BLE001 — мониторинг не критичен для прогона
        logger.exception("расчёт дрейфа не удался")
        return
    run.drift_verdict = rep["verdict"]
    run.drift_psi_max = rep["psi_max"]
    run.drift_top = ", ".join(f"{f['feature']} {f['psi']:.2f}" for f in rep["features"][:5])
    logger.info("дрейф данных: %s (max PSI %s)", rep["verdict"], rep["psi_max"])


def _persist_alerts(db, run_id: int, scores: pd.DataFrame) -> int:
    """Пишет тревоги прогона, перенося статусы с предыдущего прогона.

    Тревога — это ярус, присвоенный правилом «K сработавших окон», а не
    разовое превышение порога последним окном: одиночное окно тревогой не
    считается (эксп. #7), и именно на правиле K измерены заявленные метрики.
    """
    alerts = (scores[scores["tier"].notna()] if "tier" in scores.columns
              else scores[scores["risk"] >= get_alert_threshold()])
    if alerts.empty:
        return 0

    # unitek_id -> id скважины приложения
    well_map = dict(db.execute(select(Well.unitek_id, Well.id)).all())

    # статусы из последнего завершённого прогона (не терять разметку технолога)
    prev_run_id = db.scalar(
        select(MlRun.id)
        .where(MlRun.status == "done", MlRun.id != run_id)
        .order_by(MlRun.id.desc())
        .limit(1)
    )
    prev_status: dict[int, str] = {}
    if prev_run_id is not None:
        prev_status = {
            u: s
            for u, s in db.execute(
                select(MlAlert.unitek_id, MlAlert.status).where(
                    MlAlert.run_id == prev_run_id, MlAlert.status != "new"
                )
            )
            if u is not None
        }

    for r in alerts.itertuples():
        unitek = int(r.unitek_id) if pd.notna(r.unitek_id) else None
        db.add(
            MlAlert(
                run_id=run_id,
                ml_well_id=int(r.well_id),
                unitek_id=unitek,
                app_well_id=well_map.get(unitek),
                oilfield=r.oilfield if isinstance(r.oilfield, str) else None,
                risk=round(float(r.risk), 4),
                window_end=r.window_end.to_pydatetime() if pd.notna(r.window_end) else None,
                status=prev_status.get(unitek, "new") if unitek is not None else "new",
            )
        )
    db.commit()
    return len(alerts)


def model_health() -> dict:
    """Состояние боевой модели и вердикт «пора ли переобучать».

    Собирает наблюдаемое состояние из того, что уже есть в системе: дрейф
    последнего прогона, обратная связь технологов по тревогам и возраст
    модели. Правила вынесены в :mod:`esp.monitoring.retraining` — здесь только
    сбор чисел. Fail-soft: любая недоступная часть просто не участвует в
    решении, вердикт остаётся консервативным.
    """
    from esp.monitoring.retraining import ModelState, retraining_verdict

    drift_verdict = drift_psi = None
    reviewed = false_alerts = 0
    db = SessionLocal()
    try:
        last = db.execute(
            select(MlRun).where(MlRun.status == "done")
            .order_by(MlRun.started_at.desc()).limit(1)
        ).scalars().first()
        if last is not None:
            drift_verdict, drift_psi = last.drift_verdict, last.drift_psi_max
            alerts = db.execute(
                select(MlAlert.status).where(MlAlert.run_id == last.id)
            ).scalars().all()
            reviewed = sum(1 for s in alerts if s in ("acknowledged", "false_positive"))
            false_alerts = sum(1 for s in alerts if s == "false_positive")
    except Exception:  # noqa: BLE001 — мониторинг не критичен
        logger.exception("не удалось собрать состояние модели из БД")
    finally:
        db.close()

    age_days = None
    try:
        from esp.models.registry import list_models

        meta = next((m for m in list_models(
            models_dir=str(ML_DATA_ROOT / "models"))
            if m["name"] == "stage1_binary_v1"), None)
        if meta and meta.get("saved_at"):
            saved = datetime.fromisoformat(str(meta["saved_at"]))
            age_days = (datetime.now(saved.tzinfo) - saved).total_seconds() / 86400
    except Exception:  # noqa: BLE001
        logger.exception("не удалось определить возраст модели")

    state = ModelState(drift_verdict=drift_verdict, drift_psi_max=drift_psi,
                       alerts_reviewed=reviewed, alerts_false=false_alerts,
                       model_age_days=age_days)
    decision = retraining_verdict(state)
    return {
        "urgency": decision.urgency,
        "needed": decision.needed,
        "reasons": decision.reasons,
        "drift_verdict": drift_verdict,
        "alerts_reviewed": reviewed,
        "alerts_false": false_alerts,
        "false_alert_rate": state.false_alert_rate,
        "model_age_days": round(age_days, 1) if age_days is not None else None,
    }
