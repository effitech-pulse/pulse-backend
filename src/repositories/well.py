"""Well repository."""
from __future__ import annotations

from sqlalchemy import select

from src.models.well import Well
from src.repositories.base import BaseRepository

__all__ = ["WellRepository"]


class WellRepository(BaseRepository[Well]):
    model = Well

    def list_active(self) -> list[Well]:
        return list(self.db.scalars(select(Well).where(Well.active).order_by(Well.id)))

    def list_filtered(self, active_only: bool) -> list[Well]:
        stmt = select(Well).order_by(Well.id)
        if active_only:
            stmt = stmt.where(Well.active)
        return list(self.db.scalars(stmt))

    def get_by_unitek_id(self, unitek_id: int) -> Well | None:
        return self.db.scalars(select(Well).where(Well.unitek_id == unitek_id)).first()

    def get_by_armits_id(self, armits_id: int) -> Well | None:
        return self.db.scalars(select(Well).where(Well.armits_id == armits_id)).first()
