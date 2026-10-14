"""baseline: current schema.sql state (5 tables)

Revision ID: 0001_baseline
Revises:
Create Date: 2026-06-26

Mirrors dags/app/config/schema.sql exactly, so an existing database can be
stamped at this revision and new changes applied as ordinary migrations.
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision: str = "0001_baseline"
down_revision: str | None = None
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "well",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("unitek_id", sa.BigInteger(), nullable=False),
        sa.Column("armits_id", sa.BigInteger(), nullable=False),
        sa.Column("ngdu", sa.Integer(), nullable=False),
        sa.Column("department", sa.Integer(), nullable=False),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.PrimaryKeyConstraint("id", name="pk_well"),
        sa.UniqueConstraint("unitek_id", name="uq_well_unitek_id"),
        sa.UniqueConstraint("armits_id", name="uq_well_armits_id"),
    )

    # NOTE: baseline prediction has no surrogate PK (added in 0002).
    op.create_table(
        "prediction",
        sa.Column("well_id", sa.BigInteger(), nullable=False),
        sa.Column("status", sa.String(length=100), nullable=False),
        sa.Column("parameters", postgresql.JSONB(), nullable=False),
        sa.Column("processing_date", sa.TIMESTAMP(), nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(), nullable=False, server_default=sa.func.now()),
        sa.ForeignKeyConstraint(
            ["well_id"], ["well.id"], name="fk_predictions_well", ondelete="CASCADE"
        ),
        sa.UniqueConstraint("well_id", "processing_date", name="uq_predictions_well_processing"),
    )

    op.create_table(
        "run_archive",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("dag_id", sa.String(length=250), nullable=False),
        sa.Column("run_id", sa.String(length=250), nullable=False),
        sa.Column("execution_date", sa.TIMESTAMP(), nullable=False),
        sa.Column("well_id", sa.BigInteger(), nullable=True),
        sa.Column("ngdu", sa.Integer(), nullable=True),
        sa.Column("department", sa.Integer(), nullable=True),
        sa.Column("status_type", sa.String(length=50), nullable=False),
        sa.Column("severity", sa.String(length=20), nullable=False),
        sa.Column("source", sa.String(length=100), nullable=True),
        sa.Column("message", sa.Text(), nullable=True),
        sa.Column("parameters", postgresql.JSONB(), nullable=True),
        sa.Column("artifacts_path", sa.Text(), nullable=True),
        sa.Column("created_at", sa.TIMESTAMP(), nullable=False, server_default=sa.func.now()),
        sa.ForeignKeyConstraint(
            ["well_id"], ["well.id"], name="fk_run_archive_well", ondelete="SET NULL"
        ),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index("idx_run_archive_well", "run_archive", ["well_id"])
    op.create_index("idx_run_archive_execution", "run_archive", ["execution_date"])
    op.create_index("idx_run_archive_status", "run_archive", ["status_type"])
    op.create_index("idx_run_archive_run", "run_archive", ["dag_id", "run_id"])
    op.create_index(
        "uq_run_archive_event",
        "run_archive",
        ["dag_id", "run_id", "well_id", "source"],
        unique=True,
    )

    op.create_table(
        "complication_reference",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("well_id", sa.BigInteger(), nullable=True),
        sa.Column("code", sa.String(length=50), nullable=False),
        sa.Column("name", sa.String(length=255), nullable=False),
        sa.Column("category", sa.String(length=100), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("created_at", sa.TIMESTAMP(), nullable=False, server_default=sa.func.now()),
        sa.ForeignKeyConstraint(
            ["well_id"], ["well.id"], name="fk_complication_well", ondelete="SET NULL"
        ),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index("idx_complication_well", "complication_reference", ["well_id"])
    op.create_index("idx_complication_code", "complication_reference", ["code"])

    op.create_table(
        "department",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column("name", sa.String(length=255), nullable=False),
        sa.PrimaryKeyConstraint("id"),
    )


def downgrade() -> None:
    op.drop_table("department")
    op.drop_index("idx_complication_code", table_name="complication_reference")
    op.drop_index("idx_complication_well", table_name="complication_reference")
    op.drop_table("complication_reference")
    op.drop_index("uq_run_archive_event", table_name="run_archive")
    op.drop_index("idx_run_archive_run", table_name="run_archive")
    op.drop_index("idx_run_archive_status", table_name="run_archive")
    op.drop_index("idx_run_archive_execution", table_name="run_archive")
    op.drop_index("idx_run_archive_well", table_name="run_archive")
    op.drop_table("run_archive")
    op.drop_table("prediction")
    op.drop_table("well")
