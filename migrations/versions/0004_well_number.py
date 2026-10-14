"""add well.well_number

Revision ID: 0004_well_number
Revises: 0003_notification_rule
Create Date: 2026-06-27
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0004_well_number"
down_revision: str | None = "0003_notification_rule"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.add_column("well", sa.Column("well_number", sa.String(length=50), nullable=True))


def downgrade() -> None:
    op.drop_column("well", "well_number")
