"""recipient groups + scoped/grouped notification rules

Revision ID: 0010_notification_groups
Revises: 0009_ngdu
Create Date: 2026-06-28
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision: str = "0010_notification_groups"
down_revision: str | None = "0009_ngdu"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "recipient_group",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("name", sa.String(length=255), nullable=False),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("members", postgresql.JSONB(), nullable=False),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("created_at", sa.DateTime(), server_default=sa.func.now(), nullable=False),
        sa.PrimaryKeyConstraint("id", name="pk_recipient_group"),
        sa.UniqueConstraint("name", name="uq_recipient_group_name"),
    )

    # Scope matchers + group targets on notification_rule (all nullable = any / none).
    op.add_column("notification_rule", sa.Column("ngdu", postgresql.JSONB(), nullable=True))
    op.add_column("notification_rule", sa.Column("department", postgresql.JSONB(), nullable=True))
    op.add_column("notification_rule", sa.Column("well_ids", postgresql.JSONB(), nullable=True))
    op.add_column("notification_rule", sa.Column("group_ids", postgresql.JSONB(), nullable=True))
    # A rule may now target only groups → recipients becomes optional.
    op.alter_column(
        "notification_rule",
        "recipients",
        existing_type=sa.Text(),
        nullable=False,
        server_default="",
    )


def downgrade() -> None:
    op.alter_column(
        "notification_rule",
        "recipients",
        existing_type=sa.Text(),
        nullable=False,
        server_default=None,
    )
    op.drop_column("notification_rule", "group_ids")
    op.drop_column("notification_rule", "well_ids")
    op.drop_column("notification_rule", "department")
    op.drop_column("notification_rule", "ngdu")
    op.drop_table("recipient_group")
