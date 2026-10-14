"""Parameter dictionary loaded from parameters_dict.json.

Maps the unified DB field name (the JSON key) to the human-readable well
parameter name(s). Used by the UI to build charts with correct field/label
mapping.
"""
from __future__ import annotations

import json
from pathlib import Path

from src.schemas.parameters import ParameterRead

__all__ = ["load_parameters"]


def load_parameters(path: str) -> list[ParameterRead]:
    file = Path(path)
    if not file.exists():
        return []
    data = json.loads(file.read_text(encoding="utf-8"))
    result: list[ParameterRead] = []
    for field, variants in data.items():
        names = variants if isinstance(variants, list) else [str(variants)]
        result.append(
            ParameterRead(field=field, name=names[0] if names else field, aliases=names)
        )
    return result
