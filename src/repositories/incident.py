"""Incident repository."""
from __future__ import annotations

from datetime import date

from sqlalchemy import select

from src.models.incident import Incident
from src.repositories.base import BaseRepository

__all__ = ["IncidentRepository"]


class IncidentRepository(BaseRepository[Incident]):
    model = Incident

    def list_for_well(self, well_id: int) -> list[Incident]:
        stmt = (
            select(Incident)
            .where(Incident.well_id == well_id)
            .order_by(Incident.failure_date.desc(), Incident.id.desc())
        )
        return list(self.db.scalars(stmt))

    def list_type_dates(self) -> list[tuple[str, "date"]]:
        stmt = select(Incident.failure_type, Incident.failure_date)
        return [(t, d) for t, d in self.db.execute(stmt)]
