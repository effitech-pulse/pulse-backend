"""incident table (per-well failure cases for parser runs)

Revision ID: 0007_incident
Revises: 0006_department_user_id
Create Date: 2026-06-27
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0007_incident"
down_revision: str | None = "0006_department_user_id"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "incident",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("well_id", sa.BigInteger(), nullable=False),
        sa.Column("failure_type", sa.String(length=100), nullable=False),
        sa.Column("failure_date", sa.Date(), nullable=False),
        sa.Column("days_before", sa.Integer(), nullable=False, server_default="60"),
        sa.Column("created_at", sa.TIMESTAMP(), nullable=False, server_default=sa.func.now()),
        sa.ForeignKeyConstraint(
            ["well_id"], ["well.id"], name="fk_incident_well", ondelete="CASCADE"
        ),
        sa.PrimaryKeyConstraint("id", name="pk_incident"),
    )
    op.create_index("idx_incident_well", "incident", ["well_id"])


def downgrade() -> None:
    op.drop_index("idx_incident_well", table_name="incident")
    op.drop_table("incident")
