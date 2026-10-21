"""Доверенные синтетические fixtures Q0: serializable fake без обучения."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import pickle
from typing import Any

import numpy as np
import pandas as pd


class FixedModel:
    """Фиксированный ответ; никаких fit/train и настоящих весов."""
    feature_names_ = ['running_fraction', 'n_pump_transitions', 'CosF_below_fraction',
                      'I_in_band_fraction', 't_limit_fraction', 'I_le_xx_fraction']
    calls = 0

    def predict_proba(self, values: pd.DataFrame) -> np.ndarray:
        type(self).calls += 1
        assert np.isfinite(values.to_numpy()).all()
        probability = np.full(len(values), .1)
        return np.column_stack([1 - probability, probability])


def canonical_fixture(*, running: bool = True) -> bytes:
    """31 сутки hourly telemetry для producer/ESP synthetic fixture."""
    index = pd.date_range('2026-01-01', periods=31 * 24 + 1, freq='h', tz='UTC')
    channels = {'F_out': 50. if running else 0., 'Ia': 22. if running else 0.,
                'CosF': .87, 'Ped_load': 55., 'Pump_intake_pressure_kgs': 45.,
                'Motor_winding_temperature': 92., 'qg': 0.}
    frame = pd.DataFrame({name: value for name, value in sorted(channels.items())}, index=index)
    frame.insert(0, 'timestamp', index.strftime('%Y-%m-%dT%H:%M:%SZ'))
    return frame.to_csv(index=False, lineterminator='\n').encode()


def trusted_bundle(path: Path, *, context: str = 'legacy_v0') -> Path:
    """Создаёт только искусственные operator artifacts в temp."""
    path.mkdir(parents=True)
    (path / 'model.pkl').write_bytes(pickle.dumps(FixedModel()))
    (path / 'meta.json').write_text(json.dumps({'model_file': 'model.pkl', 'engine': 'fixed_fake',
        'classes': [0, 1], 'config': {'context_version': context}, 'saved_at': '2026-01-01T00:00:00Z'}))
    (path / 'drift_profile.json').write_text(json.dumps({'medians': {}}))
    return path


def export_fixture(root: Path) -> dict[str, Any]:
    """Экспортирует test-only registry/input/pin для subprocess/Go proof."""
    from pulse_ml.bundle_registry import import_bundle
    from pulse_ml.pinned_upload import describe_execution_pin
    root.mkdir(parents=True, exist_ok=True)
    digest = import_bundle(trusted_bundle(root / 'trusted'), root / 'registry')
    pin = describe_execution_pin('q0_synthetic', digest, registry_root=root / 'registry')
    content = canonical_fixture()
    identity = {'input_snapshot_id': 'q0-synthetic-input', 'canonical_sha256': 'sha256:' + hashlib.sha256(content).hexdigest(),
        'fingerprint_sha256': 'sha256:' + '1' * 64, 'canonical_bytes': len(content), 'source_kind': 'uploaded_file'}
    (root / 'canonical.csv').write_bytes(content)
    (root / 'pin.json').write_text(json.dumps(pin, sort_keys=True))
    (root / 'identity.json').write_text(json.dumps(identity, sort_keys=True))
    return {'pin': pin, 'identity': identity, 'bundle_manifest_sha256': 'sha256:' + hashlib.sha256(
        (root / 'registry' / digest[7:] / 'bundle_manifest.json').read_bytes()).hexdigest()}


class AlternateFixedModel(FixedModel):
    """Иная CWD-модель: счётчики десериализации/прогноза ловят loader bypass."""
    loads = 0
    calls = 0

    def __init__(self) -> None:
        self.fixture_state = True

    def __setstate__(self, state: dict[str, Any]) -> None:
        type(self).loads += 1
        self.__dict__.update(state)

    def predict_proba(self, values: pd.DataFrame) -> np.ndarray:
        type(self).calls += 1
        return np.column_stack([np.full(len(values), .01), np.full(len(values), .99)])
