"""ActionLog ORM model — audit journal of mutating API actions.

Records POST/PUT/PATCH/DELETE on /api/v1; rows older than a week are pruned.
"""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import BigInteger, DateTime, Identity, Index, Integer, String, func
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["ActionLog"]


class ActionLog(Base):
    __tablename__ = "action_log"
    __table_args__ = (Index("idx_action_log_ts", "ts"),)

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    ts: Mapped[datetime] = mapped_column(DateTime, nullable=False, server_default=func.now())
    actor: Mapped[str | None] = mapped_column(String(100))  # client IP / api-key marker
    method: Mapped[str] = mapped_column(String(10), nullable=False)
    path: Mapped[str] = mapped_column(String(300), nullable=False)
    status_code: Mapped[int] = mapped_column(Integer, nullable=False)
