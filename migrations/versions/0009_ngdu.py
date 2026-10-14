"""ngdu reference table

Revision ID: 0009_ngdu
Revises: 0008_action_log
Create Date: 2026-06-27
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0009_ngdu"
down_revision: str | None = "0008_action_log"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "ngdu",
        sa.Column("id", sa.BigInteger(), autoincrement=False, nullable=False),
        sa.Column("name", sa.String(length=255), nullable=False),
        sa.PrimaryKeyConstraint("id", name="pk_ngdu"),
    )


def downgrade() -> None:
    op.drop_table("ngdu")
