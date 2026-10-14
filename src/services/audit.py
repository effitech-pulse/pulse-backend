"""Audit-log service + middleware helper.

Records mutating API actions and keeps only the last RETENTION_DAYS.
"""
from __future__ import annotations

import logging
from datetime import datetime, timedelta

from src.core.database import SessionLocal
from src.models.action_log import ActionLog
from src.repositories.audit import AuditRepository
from src.schemas.audit import AuditRead

logger = logging.getLogger(__name__)

RETENTION_DAYS = 7

__all__ = ["AuditService", "record_action"]


class AuditService:
    def __init__(self, repo: AuditRepository) -> None:
        self._repo = repo

    def list_recent(self, limit: int = 200) -> list[AuditRead]:
        return [AuditRead.model_validate(a) for a in self._repo.list_recent(limit)]

    def record(self, actor: str | None, method: str, path: str, status_code: int) -> None:
        self._repo.add(
            ActionLog(actor=actor, method=method, path=path, status_code=status_code)
        )
        self._repo.prune(datetime.now() - timedelta(days=RETENTION_DAYS))
        self._repo.commit()


def record_action(actor: str | None, method: str, path: str, status_code: int) -> None:
    """Open a short-lived session and record one action (used by the middleware).

    Never raises — auditing must not break the request.
    """
    db = SessionLocal()
    try:
        AuditService(AuditRepository(db)).record(actor, method, path, status_code)
    except Exception as exc:  # noqa: BLE001
        logger.warning("audit record failed: %s", exc)
        db.rollback()
    finally:
        db.close()
