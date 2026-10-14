"""Parameter-dictionary DTO."""
from __future__ import annotations

from pydantic import BaseModel

__all__ = ["ParameterRead"]


class ParameterRead(BaseModel):
    field: str  # имя поля в БД (унифицированное)
    name: str  # человекочитаемое название параметра скважины
    aliases: list[str]  # варианты названий в источниках
