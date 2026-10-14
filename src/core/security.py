"""API-key authentication dependency.

When ``settings.api_key`` is set, every /api/v1 route requires the header
``X-API-Key: <key>``. Empty key (default) disables auth (localhost-friendly).
"""
from __future__ import annotations

from fastapi import Depends, Header

from src.core.config import Settings, get_settings
from src.core.exceptions import UnauthorizedError

__all__ = ["require_api_key"]


def require_api_key(
    x_api_key: str | None = Header(default=None, alias="X-API-Key"),
    settings: Settings = Depends(get_settings),
) -> None:
    if not settings.api_key:
        return  # auth disabled
    if x_api_key != settings.api_key:
        raise UnauthorizedError("Invalid or missing X-API-Key")
