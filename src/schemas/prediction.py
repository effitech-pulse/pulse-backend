"""Prediction DTOs."""
from __future__ import annotations

from datetime import date, datetime
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

__all__ = ["PredictionRead"]


class PredictionRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    well_id: int
    status: str
    parameters: dict[str, Any] = Field(default_factory=dict)
    processing_date: date
    created_at: datetime
