"""SQLAlchemy engine, session factory and declarative Base.

The API layer talks to the same database as the Airflow pipeline, but through
SQLAlchemy (not Airflow's PostgresHook), so it can run as a standalone service.
"""
from __future__ import annotations

from collections.abc import Iterator

from sqlalchemy import create_engine
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker

from src.core.config import get_settings

__all__ = ["Base", "engine", "SessionLocal", "get_db"]

_settings = get_settings()

engine = create_engine(
    _settings.database_url,
    echo=_settings.db_echo,
    pool_pre_ping=_settings.db_pool_pre_ping,
    pool_size=_settings.db_pool_size,
    future=True,
)

SessionLocal = sessionmaker(bind=engine, autoflush=False, autocommit=False, future=True)


class Base(DeclarativeBase):
    """Declarative base for all ORM models."""


def get_db() -> Iterator[Session]:
    """FastAPI dependency yielding a request-scoped session."""
    db = SessionLocal()
    try:
        yield db
    finally:
        db.close()
