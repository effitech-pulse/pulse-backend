"""Incident service (per-well failure cases)."""
from __future__ import annotations

from src.core.exceptions import NotFoundError
from src.models.incident import Incident
from src.repositories.incident import IncidentRepository
from src.schemas.incident import IncidentCreate, IncidentRead, IncidentStats

__all__ = ["IncidentService"]


class IncidentService:
    def __init__(self, repo: IncidentRepository) -> None:
        self._repo = repo

    def list_for_well(self, well_id: int) -> list[IncidentRead]:
        return [IncidentRead.model_validate(i) for i in self._repo.list_for_well(well_id)]

    def create(self, well_id: int, data: IncidentCreate) -> IncidentRead:
        incident = self._repo.add(
            Incident(
                well_id=well_id,
                failure_type=data.failure_type,
                failure_date=data.failure_date,
                days_before=data.days_before,
            )
        )
        self._repo.commit()
        return IncidentRead.model_validate(incident)

    def get(self, incident_id: int) -> IncidentRead:
        incident = self._repo.get(incident_id)
        if incident is None:
            raise NotFoundError(f"Incident {incident_id} not found")
        return IncidentRead.model_validate(incident)

    def delete(self, incident_id: int) -> None:
        incident = self._repo.get(incident_id)
        if incident is None:
            raise NotFoundError(f"Incident {incident_id} not found")
        self._repo.delete(incident)
        self._repo.commit()

    def stats(self) -> IncidentStats:
        rows = self._repo.list_type_dates()
        by_type: dict[str, int] = {}
        by_month: dict[str, int] = {}
        for ftype, fdate in rows:
            by_type[ftype] = by_type.get(ftype, 0) + 1
            key = fdate.strftime("%Y-%m")
            by_month[key] = by_month.get(key, 0) + 1
        return IncidentStats(
            total=len(rows),
            by_type=[
                {"failure_type": t, "count": c}
                for t, c in sorted(by_type.items(), key=lambda kv: -kv[1])
            ],
            by_month=[
                {"month": m, "count": c} for m, c in sorted(by_month.items())
            ],
        )
