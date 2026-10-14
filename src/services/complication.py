"""Complication service (catalogue + classification business logic)."""
from __future__ import annotations

from collections.abc import Sequence

from src.models.complication import ComplicationReference
from src.repositories.complication import ComplicationRepository
from src.schemas.complication import ComplicationRead, WellClassification

__all__ = ["ComplicationService", "classify"]

COMBINED = "комбинированный"


def classify(complications: Sequence[ComplicationReference]) -> str | None:
    """Pure classification: collapse complication categories into one verdict.

    No category -> None; a single category -> that category; several -> combined.
    """
    categories = {c.category for c in complications if c.category}
    if not categories:
        return None
    if len(categories) == 1:
        return next(iter(categories))
    return COMBINED


class ComplicationService:
    def __init__(self, repo: ComplicationRepository) -> None:
        self._repo = repo

    def list_for_well(self, well_id: int) -> list[ComplicationRead]:
        return [ComplicationRead.model_validate(c) for c in self._repo.list_for_well(well_id)]

    def classify_well(self, well_id: int) -> WellClassification:
        complications = self._repo.list_for_well(well_id)
        return WellClassification(well_id=well_id, classification=classify(complications))
