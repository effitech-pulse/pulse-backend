"""Prediction service."""
from __future__ import annotations

import io
import json
from datetime import date

from openpyxl import Workbook

from src.repositories.prediction import PredictionRepository
from src.schemas.prediction import PredictionRead

__all__ = ["PredictionService"]


class PredictionService:
    def __init__(self, repo: PredictionRepository) -> None:
        self._repo = repo

    def list_for_well(self, well_id: int, limit: int = 100) -> list[PredictionRead]:
        return [PredictionRead.model_validate(p) for p in self._repo.list_for_well(well_id, limit)]

    def get_window(
        self, well_id: int, days_count: int, reference_date: date | None = None
    ) -> list[PredictionRead]:
        ref = reference_date or date.today()
        rows = self._repo.get_window(well_id, days_count, ref)
        return [PredictionRead.model_validate(p) for p in rows]

    def export_xlsx(self, well_id: int, limit: int = 5000) -> bytes:
        """Per-well predictions to .xlsx: date, status + each parameter as a column."""
        rows = self._repo.list_for_well(well_id, limit)
        # union of parameter keys for stable columns
        param_keys: list[str] = []
        for p in rows:
            for k in (p.parameters or {}):
                if k not in param_keys:
                    param_keys.append(k)

        wb = Workbook()
        ws = wb.active
        ws.title = "predictions"
        ws.append(["processing_date", "status", *param_keys])
        for p in rows:
            params = p.parameters or {}
            values = [
                params[k] if isinstance(params.get(k), (int, float, str)) else
                (json.dumps(params[k], ensure_ascii=False) if k in params else None)
                for k in param_keys
            ]
            ws.append([p.processing_date, p.status, *values])
        buf = io.BytesIO()
        wb.save(buf)
        return buf.getvalue()
