"""FastAPI application factory.

Wires CORS, structured exception handling, a /health probe and the versioned
API router. Run with: ``uvicorn src.main:app --reload``.
"""
from __future__ import annotations

import asyncio
import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import datetime, timedelta

from fastapi import Depends, FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from sqlalchemy import text

from src.api.router import api_router
from src.core.config import get_settings
from src.core.database import engine
from src.core.exceptions import AppError
from src.core.security import require_api_key
from src.schemas.common import ErrorResponse, HealthResponse
from src.services.audit import record_action
from src.services.ml_store import start_background_run

_MUTATING = ("POST", "PUT", "PATCH", "DELETE")

logger = logging.getLogger(__name__)
settings = get_settings()


async def _ml_autorun_loop() -> None:
    """Ежесуточный автопрогон скоринга в settings.ml_autorun_hour часов."""
    while True:
        now = datetime.now()
        nxt = now.replace(hour=settings.ml_autorun_hour, minute=0, second=0, microsecond=0)
        if nxt <= now:
            nxt += timedelta(days=1)
        await asyncio.sleep((nxt - now).total_seconds())
        if start_background_run("scheduled"):
            logger.info("автопрогон ML-скоринга запущен (%02d:00)", settings.ml_autorun_hour)
        else:
            logger.info("автопрогон пропущен: прогон уже выполняется")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    logger.info("Starting %s", settings.app_name)
    autorun_task: asyncio.Task | None = None
    if settings.ml_autorun_enabled:
        autorun_task = asyncio.create_task(_ml_autorun_loop())
        logger.info("автопрогон ML включён: ежедневно в %02d:00", settings.ml_autorun_hour)
    yield
    if autorun_task is not None:
        autorun_task.cancel()
    logger.info("Shutting down %s", settings.app_name)


def create_app() -> FastAPI:
    app = FastAPI(
        title=settings.app_name,
        version="1.0.0",
        debug=settings.debug,
        lifespan=lifespan,
        openapi_tags=[
            {"name": "health", "description": "Liveness / readiness probes."},
            {"name": "wells", "description": "Wells registry."},
            {"name": "predictions", "description": "Per-well daily regime predictions."},
            {"name": "complications", "description": "Per-well complications and classification."},
            {"name": "departments", "description": "Department reference."},
            {"name": "run-archive", "description": "Pipeline run events."},
        ],
    )

    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origin_list,
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    @app.middleware("http")
    async def _audit_middleware(request: Request, call_next):
        response = await call_next(request)
        try:
            path = request.url.path
            if (
                request.method in _MUTATING
                and path.startswith(settings.api_v1_prefix)
                and not path.endswith("/audit")
            ):
                actor = request.client.host if request.client else None
                record_action(actor, request.method, path, response.status_code)
        except Exception as exc:  # pragma: no cover - auditing must not break requests
            logger.warning("audit middleware error: %s", exc)
        return response

    @app.exception_handler(AppError)
    async def _app_error_handler(request: Request, exc: AppError) -> JSONResponse:
        return JSONResponse(
            status_code=exc.status_code,
            content=ErrorResponse(code=exc.code, message=exc.message).model_dump(),
        )

    @app.exception_handler(RequestValidationError)
    async def _validation_handler(request: Request, exc: RequestValidationError) -> JSONResponse:
        return JSONResponse(
            status_code=422,
            content=ErrorResponse(
                code="validation_error", message="Request validation failed", detail=exc.errors()
            ).model_dump(),
        )

    @app.get("/health", response_model=HealthResponse, tags=["health"])
    def health() -> HealthResponse:
        db_status = "ok"
        try:
            with engine.connect() as conn:
                conn.execute(text("SELECT 1"))
        except Exception as exc:  # pragma: no cover - depends on live DB
            logger.warning("Health DB check failed: %s", exc)
            db_status = "error"
        return HealthResponse(status="ok", database=db_status)

    app.include_router(
        api_router,
        prefix=settings.api_v1_prefix,
        dependencies=[Depends(require_api_key)],
    )
    return app


app = create_app()
