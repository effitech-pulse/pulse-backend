"""department.id becomes a user-supplied code (drop IDENTITY)

Revision ID: 0006_department_user_id
Revises: 0005_drop_well_department_fk
Create Date: 2026-06-27

The department id is the CEX (shop) code matching well.department, so users set
it explicitly instead of relying on auto-increment.
"""
from __future__ import annotations

from collections.abc import Sequence

from alembic import op

revision: str = "0006_department_user_id"
down_revision: str | None = "0005_drop_well_department_fk"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute("ALTER TABLE department ALTER COLUMN id DROP IDENTITY IF EXISTS")


def downgrade() -> None:
    op.execute("ALTER TABLE department ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY")
