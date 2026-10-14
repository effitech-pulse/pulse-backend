"""Audit-log DTO."""
from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel, ConfigDict

__all__ = ["AuditRead"]


class AuditRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    ts: datetime
    actor: str | None
    method: str
    path: str
    status_code: int
