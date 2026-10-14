"""Incident (failure case) endpoints."""
from __future__ import annotations

from fastapi import APIRouter, Depends, status

from src.core.deps import get_incident_service
from src.schemas.incident import IncidentCreate, IncidentRead, IncidentStats
from src.services.incident import IncidentService

router = APIRouter(tags=["incidents"])


@router.get(
    "/wells/{well_id}/incidents",
    response_model=list[IncidentRead],
    summary="List failure cases of a well",
)
def list_incidents(
    well_id: int,
    service: IncidentService = Depends(get_incident_service),
) -> list[IncidentRead]:
    return service.list_for_well(well_id)


@router.post(
    "/wells/{well_id}/incidents",
    response_model=IncidentRead,
    status_code=201,
    summary="Add a failure case to a well",
)
def create_incident(
    well_id: int,
    payload: IncidentCreate,
    service: IncidentService = Depends(get_incident_service),
) -> IncidentRead:
    return service.create(well_id, payload)


@router.delete(
    "/incidents/{incident_id}",
    status_code=status.HTTP_204_NO_CONTENT,
    summary="Delete a failure case",
)
def delete_incident(
    incident_id: int,
    service: IncidentService = Depends(get_incident_service),
) -> None:
    service.delete(incident_id)




@router.get(
    "/incidents/stats",
    response_model=IncidentStats,
    summary="Aggregate failure stats (by type, by month) for the dashboard",
)
def incident_stats(
    service: IncidentService = Depends(get_incident_service),
) -> IncidentStats:
    return service.stats()
