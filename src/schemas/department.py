"""Department DTOs."""
from __future__ import annotations

from pydantic import BaseModel, ConfigDict

__all__ = ["DepartmentRead", "DepartmentCreate", "DepartmentUpdate"]


class DepartmentRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str


class DepartmentCreate(BaseModel):
    id: int  # = код цеха (CEX), задаётся пользователем
    name: str


class DepartmentUpdate(BaseModel):
    name: str
