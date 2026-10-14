"""ComplicationReference ORM model (per-well complication catalogue)."""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import (
    BigInteger,
    Boolean,
    DateTime,
    ForeignKey,
    Identity,
    Index,
    String,
    Text,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["ComplicationReference"]


class ComplicationReference(Base):
    __tablename__ = "complication_reference"
    __table_args__ = (
        Index("idx_complication_well", "well_id"),
        Index("idx_complication_code", "code"),
    )

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    well_id: Mapped[int | None] = mapped_column(
        BigInteger, ForeignKey("well.id", ondelete="SET NULL", name="fk_complication_well")
    )
    code: Mapped[str] = mapped_column(String(50), nullable=False)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    category: Mapped[str | None] = mapped_column(String(100))
    description: Mapped[str | None] = mapped_column(Text)
    active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
