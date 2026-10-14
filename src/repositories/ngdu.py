"""NGDU repository."""
from __future__ import annotations

from sqlalchemy import select

from src.models.ngdu import Ngdu
from src.repositories.base import BaseRepository

__all__ = ["NgduRepository"]


class NgduRepository(BaseRepository[Ngdu]):
    model = Ngdu

    def list_ordered(self) -> list[Ngdu]:
        return list(self.db.scalars(select(Ngdu).order_by(Ngdu.id)))
