"""Shared column types."""
from __future__ import annotations

from sqlalchemy import JSON
from sqlalchemy.dialects.postgresql import JSONB

__all__ = ["JSONVariant"]

# JSONB on PostgreSQL (indexable, typed), plain JSON elsewhere (e.g. SQLite tests).
JSONVariant = JSON().with_variant(JSONB(), "postgresql")
