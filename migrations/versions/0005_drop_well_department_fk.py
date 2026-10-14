"""drop fk_well_department (CEX is a free code, not a managed FK)

Revision ID: 0005_drop_well_department_fk
Revises: 0004_well_number
Create Date: 2026-06-27

Migration 0002 added an FK well.department -> department.id, but `department`
holds the external CEX shop code from the import, not a managed department.id.
The constraint blocked well creation/import (empty department table), so we drop
it. The department table stays a standalone reference (id, name).
"""
from __future__ import annotations

from collections.abc import Sequence

from alembic import op

revision: str = "0005_drop_well_department_fk"
down_revision: str | None = "0004_well_number"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.drop_constraint("fk_well_department", "well", type_="foreignkey")


def downgrade() -> None:
    op.create_foreign_key(
        "fk_well_department", "well", "department", ["department"], ["id"]
    )
