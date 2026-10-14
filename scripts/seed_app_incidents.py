"""Заливка отказов из манифеста (архив КРС) в таблицу incident приложения (esp_app).

407 реальных отказов манифеста → вкладка «Отказы» в карточке скважины.
``well_id`` приложения находится по ``unitek_id`` (таблица well уже засеяна
``scripts/seed_app_wells.py``). ``days_before`` = 14 — горизонт прогноза проекта.
Идемпотентно: пара (well_id, failure_date) не дублируется.

Запуск:
    python -m scripts.seed_app_incidents
"""

from __future__ import annotations

import logging
import os
import sys
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from esp.labeling.manifest import load_manifest_labels  # noqa: E402
from esp.labeling.weak.signatures import FailureClass  # noqa: E402

logger = logging.getLogger("seed_app_incidents")

DAYS_BEFORE = 14  # горизонт прогноза проекта (7–14 суток)


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
    from dotenv import load_dotenv
    load_dotenv(PROJECT_ROOT / ".env")
    db_url = os.environ.get(
        "DATABASE_URL", "postgresql+psycopg2://esp:esp@localhost:5433/esp_app")

    from sqlalchemy import create_engine, text

    man = load_manifest_labels(PROJECT_ROOT / "data" / "manifest" / "grant_manifest_1000.xlsx")
    type_names = {int(f): f.name for f in FailureClass}

    engine = create_engine(db_url)
    inserted = skipped = 0
    with engine.begin() as conn:
        app_well_by_unitek = dict(conn.execute(
            text("SELECT unitek_id, id FROM well")).fetchall())
        for _, meta in man.items():
            if meta["failure_type"] == int(FailureClass.NORM) or meta["failure_at"] is None:
                continue
            app_well_id = app_well_by_unitek.get(meta.get("unitek_id"))
            if app_well_id is None:
                skipped += 1
                continue
            res = conn.execute(text(
                """INSERT INTO incident (well_id, failure_type, failure_date, days_before)
                   SELECT :w, :t, :d, :n
                   WHERE NOT EXISTS (
                     SELECT 1 FROM incident WHERE well_id = :w AND failure_date = :d)"""),
                {"w": app_well_id, "t": type_names.get(meta["failure_type"], "UNKNOWN"),
                 "d": meta["failure_at"].date(), "n": DAYS_BEFORE})
            inserted += res.rowcount
    logger.info("инцидентов вставлено: %d, скважин не найдено: %d", inserted, skipped)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
