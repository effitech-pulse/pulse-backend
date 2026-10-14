"""ml_run: поля детектора дрейфа данных

Revision ID: 0012_ml_run_drift
Revises: 0011_ml_runs
Create Date: 2026-09-09
"""
from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op

revision: str = "0012_ml_run_drift"
down_revision: str | None = "0011_ml_runs"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.add_column("ml_run", sa.Column("drift_verdict", sa.String(length=20), nullable=True))
    op.add_column("ml_run", sa.Column("drift_psi_max", sa.Float(), nullable=True))
    op.add_column("ml_run", sa.Column("drift_top", sa.Text(), nullable=True))


def downgrade() -> None:
    op.drop_column("ml_run", "drift_top")
    op.drop_column("ml_run", "drift_psi_max")
    op.drop_column("ml_run", "drift_verdict")
