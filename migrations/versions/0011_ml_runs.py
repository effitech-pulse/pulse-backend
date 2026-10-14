"""ml_run + ml_alert: история прогонов модели и статусы тревог

Revision ID: 0011_ml_runs
Revises: 0010_notification_groups
Create Date: 2026-09-04
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0011_ml_runs"
down_revision: str | None = "0010_notification_groups"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "ml_run",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("started_at", sa.DateTime(), server_default=sa.func.now(), nullable=False),
        sa.Column("finished_at", sa.DateTime(), nullable=True),
        sa.Column("status", sa.String(length=20), nullable=False),
        sa.Column("trigger", sa.String(length=20), nullable=False),
        sa.Column("wells_scored", sa.Integer(), nullable=True),
        sa.Column("alerts_count", sa.Integer(), nullable=True),
        sa.Column("error", sa.Text(), nullable=True),
        sa.PrimaryKeyConstraint("id", name="pk_ml_run"),
    )
    op.create_table(
        "ml_alert",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("run_id", sa.BigInteger(), nullable=False),
        sa.Column("ml_well_id", sa.BigInteger(), nullable=False),
        sa.Column("unitek_id", sa.BigInteger(), nullable=True),
        sa.Column("app_well_id", sa.BigInteger(), nullable=True),
        sa.Column("oilfield", sa.String(length=100), nullable=True),
        sa.Column("risk", sa.Float(), nullable=False),
        sa.Column("window_end", sa.DateTime(), nullable=True),
        sa.Column("status", sa.String(length=20), nullable=False),
        sa.Column("updated_at", sa.DateTime(), server_default=sa.func.now(), nullable=False),
        sa.PrimaryKeyConstraint("id", name="pk_ml_alert"),
        sa.ForeignKeyConstraint(["run_id"], ["ml_run.id"], name="fk_ml_alert_run", ondelete="CASCADE"),
        sa.ForeignKeyConstraint(["app_well_id"], ["well.id"], name="fk_ml_alert_well", ondelete="SET NULL"),
    )
    op.create_index("idx_ml_alert_run", "ml_alert", ["run_id"])
    op.create_index("idx_ml_alert_unitek", "ml_alert", ["unitek_id"])


def downgrade() -> None:
    op.drop_table("ml_alert")
    op.drop_table("ml_run")
