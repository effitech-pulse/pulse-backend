"""Well endpoints (plus nested predictions / complications / classification)."""
from __future__ import annotations

import io

from fastapi import APIRouter, Depends, File, Query, UploadFile, status
from fastapi.responses import StreamingResponse

from src.core.deps import (
    get_complication_service,
    get_prediction_service,
    get_well_service,
)
from src.core.exceptions import ValidationAppError
from src.schemas.complication import ComplicationRead, WellClassification
from src.schemas.prediction import PredictionRead
from src.schemas.well import WellCreate, WellImportResult, WellRead, WellUpdate
from src.services.complication import ComplicationService
from src.services.prediction import PredictionService
from src.services.well import WellService

router = APIRouter(prefix="/wells", tags=["wells"])

_XLSX_SUFFIXES = (".xlsx", ".xlsm")
_XLSX_MEDIA = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"


@router.get("", response_model=list[WellRead], summary="List wells")
def list_wells(
    active_only: bool = Query(True, description="Только активные скважины."),
    service: WellService = Depends(get_well_service),
) -> list[WellRead]:
    return service.list(active_only)


@router.post(
    "/import",
    response_model=WellImportResult,
    summary="Bulk-import wells from an Excel file (NGDU, WELL_NUMBER, ARMITS_ID, UNITEK_ID, CEX)",
)
async def import_wells(
    file: UploadFile = File(...),
    service: WellService = Depends(get_well_service),
) -> WellImportResult:
    if not (file.filename or "").lower().endswith(_XLSX_SUFFIXES):
        raise ValidationAppError("Ожидается файл Excel (.xlsx)")
    content = await file.read()
    return service.import_from_xlsx(content)


@router.post("", response_model=WellRead, status_code=201, summary="Create one well manually")
def create_well(
    payload: WellCreate,
    service: WellService = Depends(get_well_service),
) -> WellRead:
    return service.create(payload)


@router.post(
    "/bulk",
    response_model=WellImportResult,
    summary="Bulk-create wells from JSON (used by the import preview after editing)",
)
def create_wells_bulk(
    payload: list[WellCreate],
    service: WellService = Depends(get_well_service),
) -> WellImportResult:
    return service.import_rows([p.model_dump() for p in payload])


@router.get("/export", summary="Export all wells to Excel (.xlsx)")
def export_wells(service: WellService = Depends(get_well_service)) -> StreamingResponse:
    content = service.export_xlsx()
    return StreamingResponse(
        io.BytesIO(content),
        media_type=_XLSX_MEDIA,
        headers={"Content-Disposition": 'attachment; filename="wells.xlsx"'},
    )


@router.get("/{well_id}", response_model=WellRead, summary="Get a well by id")
def get_well(well_id: int, service: WellService = Depends(get_well_service)) -> WellRead:
    return service.get(well_id)


@router.patch("/{well_id}", response_model=WellRead, summary="Update / deactivate a well")
def update_well(
    well_id: int,
    payload: WellUpdate,
    service: WellService = Depends(get_well_service),
) -> WellRead:
    return service.update(well_id, payload)


@router.delete("/{well_id}", status_code=status.HTTP_204_NO_CONTENT, summary="Delete a well")
def delete_well(well_id: int, service: WellService = Depends(get_well_service)) -> None:
    service.delete(well_id)


@router.get(
    "/{well_id}/predictions",
    response_model=list[PredictionRead],
    tags=["predictions"],
    summary="List recent predictions for a well",
)
def well_predictions(
    well_id: int,
    limit: int = Query(100, ge=1, le=1000),
    service: PredictionService = Depends(get_prediction_service),
) -> list[PredictionRead]:
    return service.list_for_well(well_id, limit)


@router.get(
    "/{well_id}/predictions/export",
    tags=["predictions"],
    summary="Export a well's predictions to Excel (.xlsx)",
)
def export_well_predictions(
    well_id: int,
    service: PredictionService = Depends(get_prediction_service),
) -> StreamingResponse:
    content = service.export_xlsx(well_id)
    return StreamingResponse(
        io.BytesIO(content),
        media_type=_XLSX_MEDIA,
        headers={"Content-Disposition": f'attachment; filename="well_{well_id}_predictions.xlsx"'},
    )


@router.get(
    "/{well_id}/complications",
    response_model=list[ComplicationRead],
    tags=["complications"],
    summary="List active complications for a well",
)
def well_complications(
    well_id: int,
    service: ComplicationService = Depends(get_complication_service),
) -> list[ComplicationRead]:
    return service.list_for_well(well_id)


@router.get(
    "/{well_id}/classification",
    response_model=WellClassification,
    tags=["complications"],
    summary="Classify a well by its complications",
)
def well_classification(
    well_id: int,
    service: ComplicationService = Depends(get_complication_service),
) -> WellClassification:
    return service.classify_well(well_id)
