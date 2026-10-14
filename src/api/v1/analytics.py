"""Analytics endpoints: Metabase embedding status + signed iframe URLs."""
from __future__ import annotations

from fastapi import APIRouter, Depends

from src.core.deps import get_analytics_service
from src.schemas.analytics import AnalyticsStatus, EmbedUrl
from src.services.analytics import AnalyticsService

router = APIRouter(prefix="/analytics", tags=["analytics"])


@router.get("/status", response_model=AnalyticsStatus, summary="Metabase embedding status")
def get_status(service: AnalyticsService = Depends(get_analytics_service)) -> AnalyticsStatus:
    return service.status()


@router.get(
    "/dashboards/{dashboard_id}/embed",
    response_model=EmbedUrl,
    summary="Signed iframe URL for an embeddable dashboard",
)
def get_embed_url(
    dashboard_id: int, service: AnalyticsService = Depends(get_analytics_service)
) -> EmbedUrl:
    return service.embed_url(dashboard_id)
