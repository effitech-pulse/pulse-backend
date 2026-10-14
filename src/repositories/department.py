"""Department repository."""
from __future__ import annotations

from sqlalchemy import select

from src.models.department import Department
from src.repositories.base import BaseRepository

__all__ = ["DepartmentRepository"]


class DepartmentRepository(BaseRepository[Department]):
    model = Department

    def list_ordered(self) -> list[Department]:
        return list(self.db.scalars(select(Department).order_by(Department.name)))
