"""Run-archive repository."""
from __future__ import annotations

from sqlalchemy import select

from src.models.run_archive import RunArchive
from src.repositories.base import BaseRepository

__all__ = ["RunArchiveRepository"]


class RunArchiveRepository(BaseRepository[RunArchive]):
    model = RunArchive

    def list_recent(
        self,
        *,
        dag_id: str | None = None,
        well_id: int | None = None,
        status_type: str | None = None,
        limit: int = 100,
    ) -> list[RunArchive]:
        stmt = select(RunArchive).order_by(RunArchive.execution_date.desc()).limit(limit)
        if dag_id is not None:
            stmt = stmt.where(RunArchive.dag_id == dag_id)
        if well_id is not None:
            stmt = stmt.where(RunArchive.well_id == well_id)
        if status_type is not None:
            stmt = stmt.where(RunArchive.status_type == status_type)
        return list(self.db.scalars(stmt))

    def list_for_run(self, dag_id: str, run_id: str) -> list[RunArchive]:
        stmt = (
            select(RunArchive)
            .where(RunArchive.dag_id == dag_id, RunArchive.run_id == run_id)
            .order_by(RunArchive.created_at.asc())
        )
        return list(self.db.scalars(stmt))
