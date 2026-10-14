"""Analytics service: mint signed Metabase embedding URLs.

Uses Metabase *static (signed) embedding*: the backend signs a short-lived
HS256 JWT with the Metabase "Embedding secret key" and returns an iframe URL.
The secret never reaches the browser, and only whitelisted dashboards
(``settings.metabase_dashboards``) can be embedded.
"""
from __future__ import annotations

import time

import jwt

from src.core.config import Settings
from src.core.exceptions import ExternalServiceError, NotFoundError
from src.schemas.analytics import AnalyticsStatus, DashboardInfo, EmbedUrl

__all__ = ["AnalyticsService"]


class AnalyticsService:
    def __init__(self, settings: Settings) -> None:
        self._s = settings

    def status(self) -> AnalyticsStatus:
        return AnalyticsStatus(
            enabled=self._s.metabase_enabled,
            dashboards=[DashboardInfo(**d) for d in self._s.metabase_dashboard_list],
        )

    def embed_url(self, dashboard_id: int) -> EmbedUrl:
        if not self._s.metabase_enabled:
            raise ExternalServiceError(
                "Аналитика (Metabase) не настроена", code="analytics_disabled"
            )
        allowed = {int(d["id"]) for d in self._s.metabase_dashboard_list}
        if dashboard_id not in allowed:
            raise NotFoundError(f"Дашборд {dashboard_id} не разрешён для встраивания")

        payload = {
            "resource": {"dashboard": dashboard_id},
            "params": {},
            "exp": round(time.time()) + self._s.metabase_token_ttl_s,
        }
        token = jwt.encode(payload, self._s.metabase_embed_secret, algorithm="HS256")
        site = self._s.metabase_site_url.rstrip("/")
        return EmbedUrl(url=f"{site}/embed/dashboard/{token}#bordered=true&titled=true")
