"""RecipientGroup ORM model — named groups of notification recipients.

A rule can target one or more groups instead of a raw comma-separated email
list. Members are stored as JSON ``[{"channel": "email", "address": "x@y"}]``
so the schema is channel-ready (Telegram/webhook in later phases) while phase 1
only uses ``email``.
"""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import BigInteger, Boolean, DateTime, Identity, String, Text, UniqueConstraint, func
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base
from src.models.types import JSONVariant

__all__ = ["RecipientGroup"]


class RecipientGroup(Base):
    __tablename__ = "recipient_group"
    __table_args__ = (UniqueConstraint("name", name="uq_recipient_group_name"),)

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    description: Mapped[str | None] = mapped_column(Text)
    # [{"channel": "email", "address": "x@y.ru"}, ...]
    members: Mapped[list] = mapped_column(JSONVariant, nullable=False, default=list)
    active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
