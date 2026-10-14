"""Run-archive (pipeline events) endpoints."""
from __future__ import annotations

import io

from fastapi import APIRouter, Depends, Query
from fastapi.responses import StreamingResponse

from src.core.deps import get_run_archive_service
from src.schemas.run_archive import RunArchiveRead
from src.services.run_archive import RunArchiveService

router = APIRouter(prefix="/runs", tags=["run-archive"])

_XLSX_MEDIA = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"


@router.get("", response_model=list[RunArchiveRead], summary="List recent pipeline events")
def list_runs(
    dag_id: str | None = Query(None),
    well_id: int | None = Query(None),
    status_type: str | None = Query(None),
    limit: int = Query(100, ge=1, le=1000),
    service: RunArchiveService = Depends(get_run_archive_service),
) -> list[RunArchiveRead]:
    return service.list_recent(
        dag_id=dag_id, well_id=well_id, status_type=status_type, limit=limit
    )


@router.get("/export", summary="Export pipeline events to Excel (.xlsx)")
def export_runs(
    dag_id: str | None = Query(None),
    well_id: int | None = Query(None),
    status_type: str | None = Query(None),
    service: RunArchiveService = Depends(get_run_archive_service),
) -> StreamingResponse:
    content = service.export_xlsx(dag_id=dag_id, well_id=well_id, status_type=status_type)
    return StreamingResponse(
        io.BytesIO(content),
        media_type=_XLSX_MEDIA,
        headers={"Content-Disposition": 'attachment; filename="runs.xlsx"'},
    )


@router.get(
    "/{dag_id}/{run_id}",
    response_model=list[RunArchiveRead],
    summary="List events for a specific DAG run",
)
def run_events(
    dag_id: str,
    run_id: str,
    service: RunArchiveService = Depends(get_run_archive_service),
) -> list[RunArchiveRead]:
    return service.list_for_run(dag_id, run_id)
