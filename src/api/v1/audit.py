"""Audit-log endpoint."""
from __future__ import annotations

from fastapi import APIRouter, Depends, Query

from src.core.deps import get_audit_service
from src.schemas.audit import AuditRead
from src.services.audit import AuditService

router = APIRouter(prefix="/audit", tags=["audit"])


@router.get("", response_model=list[AuditRead], summary="Recent user actions (last week)")
def list_audit(
    limit: int = Query(200, ge=1, le=1000),
    service: AuditService = Depends(get_audit_service),
) -> list[AuditRead]:
    return service.list_recent(limit)
