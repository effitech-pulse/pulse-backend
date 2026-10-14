"""Audit-log repository."""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import delete, select

from src.models.action_log import ActionLog
from src.repositories.base import BaseRepository

__all__ = ["AuditRepository"]


class AuditRepository(BaseRepository[ActionLog]):
    model = ActionLog

    def list_recent(self, limit: int = 200) -> list[ActionLog]:
        stmt = select(ActionLog).order_by(ActionLog.ts.desc(), ActionLog.id.desc()).limit(limit)
        return list(self.db.scalars(stmt))

    def prune(self, cutoff: datetime) -> None:
        self.db.execute(delete(ActionLog).where(ActionLog.ts < cutoff))
