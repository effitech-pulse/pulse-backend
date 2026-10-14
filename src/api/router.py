"""Top-level API router that aggregates all v1 sub-routers."""
from __future__ import annotations

from fastapi import APIRouter

from src.api.v1 import (
    ml,
    analytics,
    audit,
    departments,
    incidents,
    ngdu,
    parameters,
    runs,
    wells,
)

api_router = APIRouter()
api_router.include_router(wells.router)
api_router.include_router(incidents.router)
api_router.include_router(departments.router)
api_router.include_router(ngdu.router)
api_router.include_router(analytics.router)
api_router.include_router(runs.router)
api_router.include_router(parameters.router)
api_router.include_router(audit.router)
api_router.include_router(ml.router)

__all__ = ["api_router"]
