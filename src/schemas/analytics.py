"""Analytics (Metabase embedding) schemas."""
from __future__ import annotations

from pydantic import BaseModel

__all__ = ["DashboardInfo", "AnalyticsStatus", "EmbedUrl"]


class DashboardInfo(BaseModel):
    id: int
    name: str


class AnalyticsStatus(BaseModel):
    """Whether Metabase embedding is configured + the available dashboards."""

    enabled: bool
    dashboards: list[DashboardInfo]


class EmbedUrl(BaseModel):
    url: str
