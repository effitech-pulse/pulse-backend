"""Generic repository base.

Repositories own a SQLAlchemy ``Session`` and expose typed read/write helpers.
They contain no business logic — that lives in the service layer.
"""
from __future__ import annotations

from typing import Generic, TypeVar

from sqlalchemy import select
from sqlalchemy.orm import Session

from src.core.database import Base

ModelT = TypeVar("ModelT", bound=Base)

__all__ = ["BaseRepository"]


class BaseRepository(Generic[ModelT]):
    model: type[ModelT]

    def __init__(self, db: Session) -> None:
        self.db = db

    def get(self, entity_id: int) -> ModelT | None:
        return self.db.get(self.model, entity_id)

    def list(self) -> list[ModelT]:
        return list(self.db.scalars(select(self.model)))

    def add(self, entity: ModelT) -> ModelT:
        self.db.add(entity)
        self.db.flush()
        return entity

    def delete(self, entity: ModelT) -> None:
        self.db.delete(entity)
        self.db.flush()

    def commit(self) -> None:
        self.db.commit()
