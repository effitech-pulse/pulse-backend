"""Пути к внешним ML-артефактам и данным скоринга."""
from __future__ import annotations
import os
from pathlib import Path
from dotenv import load_dotenv

ROOT = Path(__file__).resolve().parents[2]
load_dotenv(ROOT / ".env")
ML_DATA_ROOT = Path(os.environ.get("ML_DATA_ROOT", str(ROOT / "data"))).expanduser().resolve()
