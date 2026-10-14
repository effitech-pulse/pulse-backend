"""Prediction repository."""
from __future__ import annotations

from datetime import date, timedelta

from sqlalchemy import select

from src.models.prediction import Prediction
from src.repositories.base import BaseRepository

__all__ = ["PredictionRepository"]


class PredictionRepository(BaseRepository[Prediction]):
    model = Prediction

    def list_for_well(self, well_id: int, limit: int = 100) -> list[Prediction]:
        stmt = (
            select(Prediction)
            .where(Prediction.well_id == well_id)
            .order_by(Prediction.processing_date.desc())
            .limit(limit)
        )
        return list(self.db.scalars(stmt))

    def get_window(self, well_id: int, days_count: int, reference_date: date) -> list[Prediction]:
        """Predictions in [reference_date - days_count, reference_date)."""
        start = reference_date - timedelta(days=days_count)
        stmt = (
            select(Prediction)
            .where(
                Prediction.well_id == well_id,
                Prediction.processing_date >= start,
                Prediction.processing_date < reference_date,
            )
            .order_by(Prediction.processing_date.desc())
        )
        return list(self.db.scalars(stmt))
