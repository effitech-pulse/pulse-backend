"""Application exception hierarchy.

These map to structured JSON error responses via the global handler in
``src/main.py``. Services raise these instead of leaking driver/HTTP errors.
"""
from __future__ import annotations

__all__ = [
    "AppError",
    "NotFoundError",
    "ConflictError",
    "ValidationAppError",
    "DatabaseError",
    "ExternalServiceError",
    "UnauthorizedError",
]


class AppError(Exception):
    """Base class for all application errors.

    Attributes:
        message: Human-readable message (safe to return to the client).
        status_code: HTTP status the API should respond with.
        code: Stable machine-readable error code.
    """

    status_code: int = 500
    code: str = "internal_error"

    def __init__(self, message: str = "Internal server error", *, code: str | None = None) -> None:
        super().__init__(message)
        self.message = message
        if code is not None:
            self.code = code


class UnauthorizedError(AppError):
    status_code = 401
    code = "unauthorized"


class NotFoundError(AppError):
    status_code = 404
    code = "not_found"


class ConflictError(AppError):
    status_code = 409
    code = "conflict"


class ValidationAppError(AppError):
    status_code = 422
    code = "validation_error"


class DatabaseError(AppError):
    status_code = 500
    code = "database_error"


class ExternalServiceError(AppError):
    """Upstream dependency (e.g. Airflow REST API) failed or is unreachable."""

    status_code = 502
    code = "external_service_error"
