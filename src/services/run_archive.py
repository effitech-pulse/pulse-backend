"""Run-archive service."""
from __future__ import annotations

import io

from openpyxl import Workbook

from src.repositories.run_archive import RunArchiveRepository
from src.schemas.run_archive import RunArchiveRead

__all__ = ["RunArchiveService"]

_EXPORT_HEADER = [
    "execution_date", "dag_id", "run_id", "well_id", "ngdu", "department",
    "status_type", "severity", "source", "message",
]


class RunArchiveService:
    def __init__(self, repo: RunArchiveRepository) -> None:
        self._repo = repo

    def list_recent(
        self,
        *,
        dag_id: str | None = None,
        well_id: int | None = None,
        status_type: str | None = None,
        limit: int = 100,
    ) -> list[RunArchiveRead]:
        rows = self._repo.list_recent(
            dag_id=dag_id, well_id=well_id, status_type=status_type, limit=limit
        )
        return [RunArchiveRead.model_validate(r) for r in rows]

    def list_for_run(self, dag_id: str, run_id: str) -> list[RunArchiveRead]:
        return [RunArchiveRead.model_validate(r) for r in self._repo.list_for_run(dag_id, run_id)]

    def export_xlsx(
        self,
        *,
        dag_id: str | None = None,
        well_id: int | None = None,
        status_type: str | None = None,
        limit: int = 5000,
    ) -> bytes:
        rows = self._repo.list_recent(
            dag_id=dag_id, well_id=well_id, status_type=status_type, limit=limit
        )
        wb = Workbook()
        ws = wb.active
        ws.title = "runs"
        ws.append(_EXPORT_HEADER)
        for r in rows:
            # execution_date — tz-naive для openpyxl (он не умеет timezone-aware)
            exec_dt = r.execution_date.replace(tzinfo=None) if r.execution_date else None
            ws.append([
                exec_dt, r.dag_id, r.run_id, r.well_id, r.ngdu, r.department,
                r.status_type, r.severity, r.source, r.message,
            ])
        buf = io.BytesIO()
        wb.save(buf)
        return buf.getvalue()
