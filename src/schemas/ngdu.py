"""NGDU DTOs."""
from __future__ import annotations

from pydantic import BaseModel, ConfigDict

__all__ = ["NgduRead", "NgduCreate", "NgduUpdate"]


class NgduRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str


class NgduCreate(BaseModel):
    id: int  # = код НГДУ, задаётся пользователем
    name: str


class NgduUpdate(BaseModel):
    name: str
