"""NGDU ORM model (reference table).

`id` is the НГДУ code supplied by the user — matches `well.ngdu`.
"""
from __future__ import annotations

from sqlalchemy import BigInteger, String
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["Ngdu"]


class Ngdu(Base):
    __tablename__ = "ngdu"

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=False)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
