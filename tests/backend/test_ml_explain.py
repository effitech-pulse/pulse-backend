"""SHAP-объяснение риска: топ-вклады признаков (нативный CatBoost TreeSHAP)."""

from __future__ import annotations

import sys
from pathlib import Path

import numpy as np
import pandas as pd

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from esp.models.gbm import GBMConfig, train_gbm  # noqa: E402
from src.services.ml_scoring import shap_top_contributions  # noqa: E402


def _catboost_model():
    rng = np.random.default_rng(0)
    X = pd.DataFrame(rng.normal(size=(300, 4)), columns=["a", "b", "c", "d"])
    y = (X["a"] > 0).astype(int).to_numpy()   # сигнал только в 'a'
    return train_gbm(X, y, GBMConfig(iterations=50)), X


def test_shap_signal_feature_ranked_first():
    trained, X = _catboost_model()
    row = X.iloc[10]
    out = shap_top_contributions(trained["model"], row, list(X.columns), top_n=4)
    assert out, "SHAP должен работать для CatBoost"
    assert out[0]["feature"] == "a"           # сигнальный признак — главный вклад
    assert abs(out[0]["shap"]) > abs(out[-1]["shap"])  # сортировка по |shap|


def test_shap_respects_top_n():
    trained, X = _catboost_model()
    out = shap_top_contributions(trained["model"], X.iloc[0], list(X.columns), top_n=2)
    assert len(out) == 2


def test_shap_fail_soft_on_non_catboost():
    class Dummy:
        pass

    out = shap_top_contributions(Dummy(), pd.Series({"a": 1.0}), ["a"])
    assert out == []
