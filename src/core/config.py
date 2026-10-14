"""Application settings loaded from environment / .env (pydantic-settings v2)."""
from __future__ import annotations

import json
import logging
from functools import lru_cache

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

logger = logging.getLogger(__name__)

__all__ = ["Settings", "get_settings"]


class Settings(BaseSettings):
    """Typed application configuration.

    Values are read from environment variables (case-insensitive) or a local
    ``.env`` file. See ``.env.example`` for the full list.
    """

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
        case_sensitive=False,
    )

    # --- App -------------------------------------------------------------
    app_name: str = "Пульс скважины — API"
    debug: bool = False
    api_v1_prefix: str = "/api/v1"

    # --- Database --------------------------------------------------------
    # Default points at the Postgres used by the Airflow pipeline (conn "pg_data").
    database_url: str = Field(
        default="postgresql+psycopg2://esp:esp@localhost:5433/esp_app",
        description="SQLAlchemy database URL.",
    )
    db_echo: bool = False
    db_pool_size: int = 5
    db_pool_pre_ping: bool = True

    # --- Auth ------------------------------------------------------------
    # If set, all /api/v1 routes require header `X-API-Key: <api_key>`.
    # Empty (default) = auth disabled (fine for localhost).
    api_key: str = ""

    # --- CORS ------------------------------------------------------------
    # Comma-separated origins, e.g. "http://localhost:3000,https://app.example.com".
    cors_origins: str = "http://localhost:3000,http://localhost:5173"


    # --- Parameter dictionary --------------------------------------------
    param_dict_path: str = "parameters_dict.json"

    # --- Analytics (Metabase static/signed embedding) --------------------
    # Empty site_url or secret = analytics disabled (page shows setup hints).
    metabase_site_url: str = Field(
        default="",
        description="Public Metabase URL reachable by the user's browser, e.g. http://localhost:3001",
    )
    metabase_embed_secret: str = Field(
        default="",
        description="Metabase 'Embedding secret key' (Admin → Embedding). Used to sign iframe JWTs.",
    )
    # JSON list of dashboards allowed for embedding (whitelist), e.g.
    #   '[{"id": 1, "name": "Обзор по скважинам"}]'
    metabase_dashboards: str = "[]"
    # Lifetime of a signed embed token (seconds). The iframe must load within it.
    metabase_token_ttl_s: int = 3600

    @property
    def cors_origin_list(self) -> list[str]:
        return [o.strip() for o in self.cors_origins.split(",") if o.strip()]

    @property
    def metabase_enabled(self) -> bool:
        return bool(self.metabase_site_url and self.metabase_embed_secret)

    @property
    def metabase_dashboard_list(self) -> list[dict]:
        """Parsed whitelist of embeddable dashboards ({id, name}); [] if invalid."""
        try:
            data = json.loads(self.metabase_dashboards or "[]")
        except (json.JSONDecodeError, TypeError):
            logger.warning("METABASE_DASHBOARDS is not valid JSON; ignoring")
            return []
        if not isinstance(data, list):
            return []
        return [d for d in data if isinstance(d, dict) and "id" in d and "name" in d]



    # --- Автопрогон ML-скоринга -------------------------------------------
    ml_autorun_enabled: bool = True
    ml_autorun_hour: int = 5  # локальный час суток для ежедневного прогона


@lru_cache
def get_settings() -> Settings:
    """Return a cached Settings instance (single read per process)."""
    return Settings()
