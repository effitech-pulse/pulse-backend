"""schema improvements from the audit (Phase 1.5)

Revision ID: 0002_schema_improvements
Revises: 0001_baseline
Create Date: 2026-06-26

Applies the audit findings, all reversible:
  * prediction: add surrogate IDENTITY primary key (id);
  * prediction: index (well_id, processing_date) backing the hot query and FK;
  * prediction: processing_date TIMESTAMP -> DATE (one prediction per day);
  * well: add real FK well.department -> department.id.

CAVEAT: the processing_date TIMESTAMP->DATE change drops the time component.
Downgrade restores the column type but the original time-of-day is not
recoverable. Review on production data before applying.
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0002_schema_improvements"
down_revision: str | None = "0001_baseline"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    # 1. prediction surrogate PK
    op.add_column(
        "prediction",
        sa.Column("id", sa.BigInteger(), sa.Identity(always=True), nullable=False),
    )
    op.create_primary_key("pk_prediction", "prediction", ["id"])

    # 2. index backing get_window / FK lookups
    op.create_index(
        "idx_prediction_well_date", "prediction", ["well_id", "processing_date"]
    )

    # 3. processing_date -> DATE (daily granularity)
    op.alter_column(
        "prediction",
        "processing_date",
        type_=sa.Date(),
        existing_nullable=False,
        postgresql_using="processing_date::date",
    )

    # 4. real FK well.department -> department.id
    op.create_foreign_key(
        "fk_well_department", "well", "department", ["department"], ["id"]
    )


def downgrade() -> None:
    op.drop_constraint("fk_well_department", "well", type_="foreignkey")
    op.alter_column(
        "prediction",
        "processing_date",
        type_=sa.TIMESTAMP(),
        existing_nullable=False,
        postgresql_using="processing_date::timestamp",
    )
    op.drop_index("idx_prediction_well_date", table_name="prediction")
    op.drop_constraint("pk_prediction", "prediction", type_="primary")
    op.drop_column("prediction", "id")
