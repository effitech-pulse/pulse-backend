"""SQLAlchemy ORM models.

Importing this package registers every table on ``Base.metadata`` so Alembic
autogenerate and ``create_all`` see the full schema.
"""
from src.models.action_log import ActionLog
from src.models.complication import ComplicationReference
from src.models.department import Department
from src.models.incident import Incident
from src.models.ml_run import MlAlert, MlRun
from src.models.ngdu import Ngdu
from src.models.notification_rule import NotificationRule
from src.models.prediction import Prediction
from src.models.recipient_group import RecipientGroup
from src.models.run_archive import RunArchive
from src.models.well import Well

__all__ = [
    "Well",
    "Prediction",
    "RunArchive",
    "ComplicationReference",
    "Department",
    "NotificationRule",
    "Incident",
    "ActionLog",
    "Ngdu",
    "MlRun",
    "MlAlert",
    "RecipientGroup",
]
