"""Well DTOs."""
from __future__ import annotations

from pydantic import BaseModel, ConfigDict

__all__ = ["WellRead", "WellCreate", "WellUpdate", "WellImportResult"]


class WellBase(BaseModel):
    unitek_id: int
    armits_id: int
    well_number: str | None = None
    ngdu: int
    department: int
    active: bool = True


class WellCreate(WellBase):
    """Payload to register a new well."""


class WellUpdate(BaseModel):
    """Partial update — only provided fields are changed."""

    unitek_id: int | None = None
    armits_id: int | None = None
    well_number: str | None = None
    ngdu: int | None = None
    department: int | None = None
    active: bool | None = None


class WellRead(WellBase):
    model_config = ConfigDict(from_attributes=True)

    id: int


class WellImportResult(BaseModel):
    """Outcome of an Excel bulk import."""

    created: int = 0
    skipped: int = 0  # duplicates (existing unitek_id/armits_id)
    errors: list[str] = []
