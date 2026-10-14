"""RunArchive ORM model (events emitted by each pipeline run)."""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import (
    BigInteger,
    DateTime,
    ForeignKey,
    Identity,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base
from src.models.types import JSONVariant

__all__ = ["RunArchive"]


class RunArchive(Base):
    __tablename__ = "run_archive"
    __table_args__ = (
        Index("idx_run_archive_well", "well_id"),
        Index("idx_run_archive_execution", "execution_date"),
        Index("idx_run_archive_status", "status_type"),
        Index("idx_run_archive_run", "dag_id", "run_id"),
        UniqueConstraint("dag_id", "run_id", "well_id", "source", name="uq_run_archive_event"),
    )

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    dag_id: Mapped[str] = mapped_column(String(250), nullable=False)
    run_id: Mapped[str] = mapped_column(String(250), nullable=False)
    execution_date: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    well_id: Mapped[int | None] = mapped_column(
        BigInteger, ForeignKey("well.id", ondelete="SET NULL", name="fk_run_archive_well")
    )
    ngdu: Mapped[int | None] = mapped_column(Integer)
    department: Mapped[int | None] = mapped_column(Integer)
    status_type: Mapped[str] = mapped_column(String(50), nullable=False)
    severity: Mapped[str] = mapped_column(String(20), nullable=False)
    source: Mapped[str | None] = mapped_column(String(100))
    message: Mapped[str | None] = mapped_column(Text)
    parameters: Mapped[dict | None] = mapped_column(JSONVariant)
    artifacts_path: Mapped[str | None] = mapped_column(Text)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, nullable=False, server_default=func.now()
    )
