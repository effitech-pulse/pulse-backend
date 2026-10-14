"""notification_rule table (UI-editable notification routing)

Revision ID: 0003_notification_rule
Revises: 0002_schema_improvements
Create Date: 2026-06-27
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision: str = "0003_notification_rule"
down_revision: str | None = "0002_schema_improvements"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "notification_rule",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("status_types", postgresql.JSONB(), nullable=False),
        sa.Column("severity", sa.String(length=50), nullable=True),
        sa.Column("recipients", sa.Text(), nullable=False),
        sa.Column("subject_template", sa.String(length=500), nullable=False),
        sa.Column("priority", sa.Integer(), nullable=False, server_default="100"),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("created_at", sa.TIMESTAMP(), nullable=False, server_default=sa.func.now()),
        sa.PrimaryKeyConstraint("id", name="pk_notification_rule"),
    )
    op.create_index("idx_notification_rule_priority", "notification_rule", ["priority"])


def downgrade() -> None:
    op.drop_index("idx_notification_rule_priority", table_name="notification_rule")
    op.drop_table("notification_rule")
