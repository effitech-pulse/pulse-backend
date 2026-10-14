"""Complication repository."""
from __future__ import annotations

from sqlalchemy import select

from src.models.complication import ComplicationReference
from src.repositories.base import BaseRepository

__all__ = ["ComplicationRepository"]


class ComplicationRepository(BaseRepository[ComplicationReference]):
    model = ComplicationReference

    def list_for_well(self, well_id: int) -> list[ComplicationReference]:
        stmt = (
            select(ComplicationReference)
            .where(
                ComplicationReference.well_id == well_id,
                ComplicationReference.active,
            )
            .order_by(ComplicationReference.created_at.desc())
        )
        return list(self.db.scalars(stmt))
