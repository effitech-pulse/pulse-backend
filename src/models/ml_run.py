"""ORM-модели истории ML-прогонов.

``MlRun`` — один прогон модели по фонду (ручной или по расписанию).
``MlAlert`` — тревога прогона (риск >= порога) со статусом обратной связи
технолога: new → acknowledged (принято в работу) | false_positive (ложная).
Статус переносится на следующий прогон по unitek_id, чтобы разметка
не терялась при пересчёте.
"""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import (
    BigInteger,
    DateTime,
    Float,
    ForeignKey,
    Identity,
    Index,
    Integer,
    String,
    Text,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["MlRun", "MlAlert"]


class MlRun(Base):
    __tablename__ = "ml_run"

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    started_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
    finished_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    #: running | done | error
    status: Mapped[str] = mapped_column(String(20), nullable=False, default="running")
    #: manual | scheduled
    trigger: Mapped[str] = mapped_column(String(20), nullable=False, default="manual")
    wells_scored: Mapped[int | None] = mapped_column(Integer, nullable=True)
    alerts_count: Mapped[int | None] = mapped_column(Integer, nullable=True)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    #: дрейф данных относительно обучающей выборки: stable | moderate | severe | unknown
    drift_verdict: Mapped[str | None] = mapped_column(String(20), nullable=True)
    #: максимальный PSI по признакам (0 — распределения совпадают)
    drift_psi_max: Mapped[float | None] = mapped_column(Float, nullable=True)
    #: признаки с наибольшим дрейфом, «имя PSI» через запятую
    drift_top: Mapped[str | None] = mapped_column(Text, nullable=True)


class MlAlert(Base):
    __tablename__ = "ml_alert"
    __table_args__ = (
        Index("idx_ml_alert_run", "run_id"),
        Index("idx_ml_alert_unitek", "unitek_id"),
    )

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    run_id: Mapped[int] = mapped_column(
        BigInteger,
        ForeignKey("ml_run.id", ondelete="CASCADE", name="fk_ml_alert_run"),
        nullable=False,
    )
    #: id скважины в ML-датасете (индекс манифеста)
    ml_well_id: Mapped[int] = mapped_column(BigInteger, nullable=False)
    unitek_id: Mapped[int | None] = mapped_column(BigInteger, nullable=True)
    #: id скважины в таблице well приложения (если сматчилась по unitek_id)
    app_well_id: Mapped[int | None] = mapped_column(
        BigInteger,
        ForeignKey("well.id", ondelete="SET NULL", name="fk_ml_alert_well"),
        nullable=True,
    )
    oilfield: Mapped[str | None] = mapped_column(String(100), nullable=True)
    risk: Mapped[float] = mapped_column(Float, nullable=False)
    window_end: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    #: new | acknowledged | false_positive
    status: Mapped[str] = mapped_column(String(20), nullable=False, default="new")
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now(), onupdate=func.now()
    )
