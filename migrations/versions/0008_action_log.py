"""action_log table (user action audit journal)

Revision ID: 0008_action_log
Revises: 0007_incident
Create Date: 2026-06-27
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0008_action_log"
down_revision: str | None = "0007_incident"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "action_log",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("ts", sa.TIMESTAMP(), nullable=False, server_default=sa.func.now()),
        sa.Column("actor", sa.String(length=100), nullable=True),
        sa.Column("method", sa.String(length=10), nullable=False),
        sa.Column("path", sa.String(length=300), nullable=False),
        sa.Column("status_code", sa.Integer(), nullable=False),
        sa.PrimaryKeyConstraint("id", name="pk_action_log"),
    )
    op.create_index("idx_action_log_ts", "action_log", ["ts"])


def downgrade() -> None:
    op.drop_index("idx_action_log_ts", table_name="action_log")
    op.drop_table("action_log")
