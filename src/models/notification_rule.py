"""NotificationRule ORM model — routing rules editable from the UI.

Mirrors `config.yaml::notifications.routing` but stored in the DB so both the API
and the Airflow notifier read the same source (DB first, YAML as fallback).
"""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import BigInteger, Boolean, DateTime, Identity, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base
from src.models.types import JSONVariant

__all__ = ["NotificationRule"]


class NotificationRule(Base):
    __tablename__ = "notification_rule"

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    # Matched status_type values, e.g. ["failure_detected"] or ["no_data","invalid_data"].
    status_types: Mapped[list] = mapped_column(JSONVariant, nullable=False)
    # Optional severity match (None = any).
    severity: Mapped[str | None] = mapped_column(String(50))
    # Organisational scope matchers — null/empty = any (no restriction).
    ngdu: Mapped[list | None] = mapped_column(JSONVariant)
    department: Mapped[list | None] = mapped_column(JSONVariant)
    well_ids: Mapped[list | None] = mapped_column(JSONVariant)
    # Targets: raw comma-separated emails AND/OR recipient-group ids.
    recipients: Mapped[str] = mapped_column(Text, nullable=False, default="")
    group_ids: Mapped[list | None] = mapped_column(JSONVariant)
    subject_template: Mapped[str] = mapped_column(String(500), nullable=False)
    # Lower = evaluated first (first match wins, like the YAML order).
    priority: Mapped[int] = mapped_column(Integer, nullable=False, default=100)
    active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
