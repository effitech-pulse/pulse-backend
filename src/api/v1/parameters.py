"""Parameter dictionary endpoint."""
from __future__ import annotations

from fastapi import APIRouter, Depends

from src.core.config import Settings, get_settings
from src.schemas.parameters import ParameterRead
from src.services.parameters import load_parameters

router = APIRouter(prefix="/parameters", tags=["parameters"])


@router.get("", response_model=list[ParameterRead], summary="Parameter dictionary")
def list_parameters(settings: Settings = Depends(get_settings)) -> list[ParameterRead]:
    return load_parameters(settings.param_dict_path)
