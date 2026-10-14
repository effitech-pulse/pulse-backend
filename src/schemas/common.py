"""Shared API schemas."""
from __future__ import annotations

from typing import Any

from pydantic import BaseModel, Field

__all__ = ["ErrorResponse", "HealthResponse"]


class ErrorResponse(BaseModel):
    """Structured error body returned by the global exception handler."""

    code: str = Field(description="Stable machine-readable error code.")
    message: str = Field(description="Human-readable error message.")
    detail: Any | None = Field(default=None, description="Optional extra context.")


class HealthResponse(BaseModel):
    status: str = Field(description="'ok' if the service is healthy.")
    database: str = Field(description="'ok' or 'error' for DB connectivity.")
    version: str = Field(default="1.0.0")
