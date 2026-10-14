"""Prediction ORM model.

Target state vs the original schema.sql:
  * a surrogate ``id`` PK is added (ORM requires a PK; also enables clean FKs),
  * ``processing_date`` becomes DATE (the pipeline keys one prediction per day),
  * an explicit index on (well_id, processing_date) backs the hot query.
Migration 0002 evolves the baseline DB to this shape.
"""
from __future__ import annotations

from datetime import date, datetime

from sqlalchemy import (
    BigInteger,
    Date,
    DateTime,
    ForeignKey,
    Identity,
    Index,
    String,
    UniqueConstraint,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base
from src.models.types import JSONVariant

__all__ = ["Prediction"]


class Prediction(Base):
    __tablename__ = "prediction"
    __table_args__ = (
        UniqueConstraint("well_id", "processing_date", name="uq_predictions_well_processing"),
        Index("idx_prediction_well_date", "well_id", "processing_date"),
    )

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    well_id: Mapped[int] = mapped_column(
        BigInteger,
        ForeignKey("well.id", ondelete="CASCADE", name="fk_predictions_well"),
        nullable=False,
    )
    status: Mapped[str] = mapped_column(String(100), nullable=False)
    parameters: Mapped[dict] = mapped_column(JSONVariant, nullable=False)
    processing_date: Mapped[date] = mapped_column(Date, nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
