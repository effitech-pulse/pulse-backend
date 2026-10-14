"""Incident (failure case) DTOs."""
from __future__ import annotations

from datetime import date, datetime

from pydantic import BaseModel, ConfigDict, Field

__all__ = ["IncidentRead", "IncidentCreate", "IncidentStats"]


class IncidentBase(BaseModel):
    failure_type: str = Field(description="Тип/класс поломки.")
    failure_date: date = Field(description="Дата (период) возникновения отказа.")
    days_before: int = Field(default=60, ge=1, le=365, description="Окно истории до отказа, дней.")


class IncidentCreate(IncidentBase):
    pass


class IncidentRead(IncidentBase):
    model_config = ConfigDict(from_attributes=True)

    id: int
    well_id: int
    created_at: datetime


class TypeCount(BaseModel):
    failure_type: str
    count: int


class MonthCount(BaseModel):
    month: str = Field(description="Месяц в формате YYYY-MM.")
    count: int


class IncidentStats(BaseModel):
    """Агрегаты по всем отказам — для дашборда."""

    total: int
    by_type: list[TypeCount]
    by_month: list[MonthCount]
