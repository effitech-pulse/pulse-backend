"""Department service (reference CRUD)."""
from __future__ import annotations

from src.core.exceptions import ConflictError, NotFoundError
from src.models.department import Department
from src.repositories.department import DepartmentRepository
from src.schemas.department import DepartmentCreate, DepartmentRead, DepartmentUpdate

__all__ = ["DepartmentService"]


class DepartmentService:
    def __init__(self, repo: DepartmentRepository) -> None:
        self._repo = repo

    def list(self) -> list[DepartmentRead]:
        return [DepartmentRead.model_validate(d) for d in self._repo.list_ordered()]

    def create(self, data: DepartmentCreate) -> DepartmentRead:
        if self._repo.get(data.id) is not None:
            raise ConflictError(f"Департамент с id={data.id} уже существует")
        dep = self._repo.add(Department(id=data.id, name=data.name))
        self._repo.commit()
        return DepartmentRead.model_validate(dep)

    def update(self, dep_id: int, data: DepartmentUpdate) -> DepartmentRead:
        dep = self._repo.get(dep_id)
        if dep is None:
            raise NotFoundError(f"Department {dep_id} not found")
        dep.name = data.name
        self._repo.commit()
        return DepartmentRead.model_validate(dep)

    def delete(self, dep_id: int) -> None:
        dep = self._repo.get(dep_id)
        if dep is None:
            raise NotFoundError(f"Department {dep_id} not found")
        self._repo.delete(dep)
        self._repo.commit()
