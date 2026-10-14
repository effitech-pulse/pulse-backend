"""NGDU service (reference CRUD)."""
from __future__ import annotations

from src.core.exceptions import ConflictError, NotFoundError
from src.models.ngdu import Ngdu
from src.repositories.ngdu import NgduRepository
from src.schemas.ngdu import NgduCreate, NgduRead, NgduUpdate

__all__ = ["NgduService"]


class NgduService:
    def __init__(self, repo: NgduRepository) -> None:
        self._repo = repo

    def list(self) -> list[NgduRead]:
        return [NgduRead.model_validate(n) for n in self._repo.list_ordered()]

    def create(self, data: NgduCreate) -> NgduRead:
        if self._repo.get(data.id) is not None:
            raise ConflictError(f"НГДУ с id={data.id} уже существует")
        ngdu = self._repo.add(Ngdu(id=data.id, name=data.name))
        self._repo.commit()
        return NgduRead.model_validate(ngdu)

    def update(self, ngdu_id: int, data: NgduUpdate) -> NgduRead:
        ngdu = self._repo.get(ngdu_id)
        if ngdu is None:
            raise NotFoundError(f"NGDU {ngdu_id} not found")
        ngdu.name = data.name
        self._repo.commit()
        return NgduRead.model_validate(ngdu)

    def delete(self, ngdu_id: int) -> None:
        ngdu = self._repo.get(ngdu_id)
        if ngdu is None:
            raise NotFoundError(f"NGDU {ngdu_id} not found")
        self._repo.delete(ngdu)
        self._repo.commit()
