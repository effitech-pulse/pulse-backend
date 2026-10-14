"""Run-archive (pipeline event) DTOs."""
from __future__ import annotations

from datetime import datetime
from typing import Any

from pydantic import BaseModel, ConfigDict

__all__ = ["RunArchiveRead"]


class RunArchiveRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    dag_id: str
    run_id: str
    execution_date: datetime
    well_id: int | None
    ngdu: int | None
    department: int | None
    status_type: str
    severity: str
    source: str | None
    message: str | None
    parameters: dict[str, Any] | None
    artifacts_path: str | None
    created_at: datetime
