"""Подписи признаков в объяснении алерта: каждый алерт должен быть читаем."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))


def test_feature_label_covers_derived_features():
    """Производные признаки не должны доезжать до технолога техническим именем.

    Подписи перечислялись вручную, и каждый новый признак с суффиксом
    (нормировка на месторождение, отклонение от своего режима, тренд при
    постоянной частоте) появлялся в объяснении алерта как «steady_fraction_field_z».
    """
    from src.api.v1.ml import feature_label

    assert feature_label("debit_qg_last") == "Дебит жидкости (факт)"
    assert feature_label("steady_fraction_field_z").startswith("Доля времени без изменений")
    assert "к уровню месторождения" in feature_label("current_fluctuation_field_z")
    # у running_fraction_vs_own_base есть своя явная подпись — она и побеждает
    assert feature_label("running_fraction_vs_own_base") == "Работа: отклонение от своего режима"
    assert "к своему режиму" in feature_label("n_pump_transitions_vs_own_base")
    assert "постоянной частоте" in feature_label("Pump_intake_pressure_kgs_const_f_slope")
    # незнакомый признак возвращается как есть, без выдумывания подписи
    assert feature_label("какой_то_новый") == "какой_то_новый"


def test_every_production_feature_has_a_human_label():
    """Каждый признак боевой модели объясним по-русски — требование приёмки."""
    from esp.models.registry import load_model
    from src.api.v1.ml import feature_label

    try:
        model = load_model("stage1_binary_v1", models_dir="data/models")["model"]
    except Exception:  # noqa: BLE001 — в окружении без обученной модели тест не применим
        pytest.skip("боевая модель не найдена")
    unlabeled = [f for f in model.feature_names_ if feature_label(f) == f]
    assert unlabeled == []
