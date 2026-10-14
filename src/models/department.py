"""Department ORM model (reference table).

`id` is the shop (цех) code supplied by the user — it matches `well.department`
(the CEX value from the import), so the reference maps code -> human name.
"""
from __future__ import annotations

from sqlalchemy import BigInteger, String
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["Department"]


class Department(Base):
    __tablename__ = "department"

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=False)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
