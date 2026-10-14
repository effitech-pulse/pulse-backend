"""Incident ORM model — a configured failure case for a well.

Drives parser/pipeline runs: failure_date + days_before define the history
window to pull. Stored per well so the UI «Отказы» tab can list and re-run them.
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
    Integer,
    String,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["Incident"]


class Incident(Base):
    __tablename__ = "incident"
    __table_args__ = (Index("idx_incident_well", "well_id"),)

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    well_id: Mapped[int] = mapped_column(
        BigInteger, ForeignKey("well.id", ondelete="CASCADE", name="fk_incident_well"), nullable=False
    )
    failure_type: Mapped[str] = mapped_column(String(100), nullable=False)
    failure_date: Mapped[date] = mapped_column(Date, nullable=False)
    days_before: Mapped[int] = mapped_column(Integer, nullable=False, default=60)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
