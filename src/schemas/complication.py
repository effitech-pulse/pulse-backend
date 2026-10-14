"""Complication DTOs."""
from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel, ConfigDict

__all__ = ["ComplicationRead", "WellClassification"]


class ComplicationRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    well_id: int | None
    code: str
    name: str
    category: str | None
    description: str | None
    active: bool
    created_at: datetime


class WellClassification(BaseModel):
    """Result of classifying a well by its complications."""

    well_id: int
    classification: str | None
