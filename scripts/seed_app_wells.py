"""Заливка скважин из манифеста (архив КРС) в таблицу well приложения (БД esp_app).

Мост между исследовательским контуром (манифест 1000 скважин) и операционным
приложением (src/ backend, перенос из esp_failure_predictor). Цех (department)
в манифесте отсутствует — пишем 0 («не задан»; FK на department снят миграцией
0005, справочник заполняется позже). Идемпотентно: конфликт по unitek_id/armits_id
пропускается.

Запуск:
    python -m scripts.seed_app_wells
"""

from __future__ import annotations

import logging
import os
import sys
from pathlib import Path

import pandas as pd

PROJECT_ROOT = Path(__file__).resolve().parents[1]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

logger = logging.getLogger("seed_app_wells")

APP_DB_URL = os.environ.get(
    "DATABASE_URL", "postgresql+psycopg2://esp:esp@localhost:5433/esp_app")


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
    from dotenv import load_dotenv
    load_dotenv(PROJECT_ROOT / ".env")
    db_url = os.environ.get("DATABASE_URL", APP_DB_URL)

    from sqlalchemy import create_engine, text

    man = pd.read_excel(PROJECT_ROOT / "data" / "manifest" / "grant_manifest_1000.xlsx")
    rows = []
    for _, r in man.iterrows():
        if pd.isna(r.get("UNITEK_ID")) or pd.isna(r.get("ARMITS_ID")):
            continue
        rows.append({
            "unitek_id": int(r["UNITEK_ID"]),
            "armits_id": int(r["ARMITS_ID"]),
            "well_number": str(r["WELL_NUMBER"]) if pd.notna(r.get("WELL_NUMBER")) else None,
            "ngdu": int(r["NGDU"]) if pd.notna(r.get("NGDU")) else 0,
            "department": 0,
        })
    logger.info("скважин в манифесте к заливке: %d", len(rows))

    engine = create_engine(db_url)
    inserted = 0
    with engine.begin() as conn:
        for w in rows:
            res = conn.execute(text(
                """INSERT INTO well (unitek_id, armits_id, well_number, ngdu, department, active)
                   VALUES (:unitek_id, :armits_id, :well_number, :ngdu, :department, true)
                   ON CONFLICT (unitek_id) DO NOTHING"""), w)
            inserted += res.rowcount
        n_ngdu = conn.execute(text(
            """INSERT INTO ngdu (id, name)
               SELECT DISTINCT ngdu, 'НГДУ-' || ngdu FROM well
               ON CONFLICT (id) DO NOTHING""")).rowcount
        conn.execute(text(
            """INSERT INTO department (id, name)
               VALUES (0, 'Цех не указан (ждём справочник ЦДНГ)')
               ON CONFLICT (id) DO NOTHING"""))
    logger.info("вставлено скважин: %d, справочник НГДУ: +%d", inserted, n_ngdu)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
