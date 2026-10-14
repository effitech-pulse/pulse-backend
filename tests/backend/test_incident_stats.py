"""Агрегаты отказов для дашборда: IncidentService.stats()."""

from __future__ import annotations

import sys
from datetime import date
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from src.services.incident import IncidentService  # noqa: E402


class _FakeRepo:
    def __init__(self, rows: list[tuple[str, date]]) -> None:
        self._rows = rows

    def list_type_dates(self) -> list[tuple[str, date]]:
        return self._rows


def test_stats_aggregates_by_type_and_month() -> None:
    rows = [
        ("PARAFFIN", date(2025, 3, 5)),
        ("PARAFFIN", date(2025, 3, 20)),
        ("SCALING", date(2025, 4, 1)),
    ]
    stats = IncidentService(_FakeRepo(rows)).stats()  # type: ignore[arg-type]

    assert stats.total == 3
    # Сортировка по убыванию количества.
    assert stats.by_type[0].failure_type == "PARAFFIN"
    assert stats.by_type[0].count == 2
    # Месяцы в хронологическом порядке, ключ YYYY-MM.
    assert [m.month for m in stats.by_month] == ["2025-03", "2025-04"]
    assert [m.count for m in stats.by_month] == [2, 1]


def test_stats_empty() -> None:
    stats = IncidentService(_FakeRepo([])).stats()  # type: ignore[arg-type]
    assert stats.total == 0
    assert stats.by_type == []
    assert stats.by_month == []
