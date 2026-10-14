"""FastAPI dependency-injection providers.

Each ``get_*_service`` wires a request-scoped DB session into a repository and
then into a service, so routers depend only on the service interface.
"""
from __future__ import annotations

from fastapi import Depends
from sqlalchemy.orm import Session

from src.core.config import Settings, get_settings
from src.core.database import get_db
from src.repositories.audit import AuditRepository
from src.repositories.complication import ComplicationRepository
from src.repositories.department import DepartmentRepository
from src.repositories.incident import IncidentRepository
from src.repositories.ngdu import NgduRepository
from src.repositories.prediction import PredictionRepository
from src.repositories.run_archive import RunArchiveRepository
from src.repositories.well import WellRepository
from src.services.analytics import AnalyticsService
from src.services.audit import AuditService
from src.services.complication import ComplicationService
from src.services.department import DepartmentService
from src.services.incident import IncidentService
from src.services.ngdu import NgduService
from src.services.prediction import PredictionService
from src.services.run_archive import RunArchiveService
from src.services.well import WellService

__all__ = [
    "get_well_service",
    "get_prediction_service",
    "get_department_service",
    "get_complication_service",
    "get_run_archive_service",
    "get_incident_service",
    "get_audit_service",
    "get_ngdu_service",
    "get_analytics_service",
]


def get_well_service(db: Session = Depends(get_db)) -> WellService:
    return WellService(WellRepository(db))


def get_prediction_service(db: Session = Depends(get_db)) -> PredictionService:
    return PredictionService(PredictionRepository(db))


def get_department_service(db: Session = Depends(get_db)) -> DepartmentService:
    return DepartmentService(DepartmentRepository(db))


def get_complication_service(db: Session = Depends(get_db)) -> ComplicationService:
    return ComplicationService(ComplicationRepository(db))


def get_run_archive_service(db: Session = Depends(get_db)) -> RunArchiveService:
    return RunArchiveService(RunArchiveRepository(db))




def get_incident_service(db: Session = Depends(get_db)) -> IncidentService:
    return IncidentService(IncidentRepository(db))


def get_audit_service(db: Session = Depends(get_db)) -> AuditService:
    return AuditService(AuditRepository(db))


def get_ngdu_service(db: Session = Depends(get_db)) -> NgduService:
    return NgduService(NgduRepository(db))


def get_analytics_service(settings: Settings = Depends(get_settings)) -> AnalyticsService:
    return AnalyticsService(settings)

