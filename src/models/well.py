"""Well ORM model."""
from __future__ import annotations

from sqlalchemy import BigInteger, Boolean, Identity, Integer, String
from sqlalchemy.orm import Mapped, mapped_column

from src.core.database import Base

__all__ = ["Well"]


class Well(Base):
    __tablename__ = "well"

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    unitek_id: Mapped[int] = mapped_column(BigInteger, unique=True, nullable=False)
    armits_id: Mapped[int] = mapped_column(BigInteger, unique=True, nullable=False)
    # Человекочитаемый номер скважины (необязательный, из импорта WELL_NUMBER).
    well_number: Mapped[str | None] = mapped_column(String(50))
    ngdu: Mapped[int] = mapped_column(Integer, nullable=False)
    # Код цеха (CEX из импорта). Логически соответствует department.id, но без
    # жёсткого FK: CEX — внешний код, департаменты ведутся отдельным справочником.
    department: Mapped[int] = mapped_column(Integer, nullable=False)
    active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
