"""NGDU reference endpoints (id = НГДУ code)."""
from __future__ import annotations

from fastapi import APIRouter, Depends, status

from src.core.deps import get_ngdu_service
from src.schemas.ngdu import NgduCreate, NgduRead, NgduUpdate
from src.services.ngdu import NgduService

router = APIRouter(prefix="/ngdu", tags=["ngdu"])


@router.get("", response_model=list[NgduRead], summary="List NGDU")
def list_ngdu(service: NgduService = Depends(get_ngdu_service)) -> list[NgduRead]:
    return service.list()


@router.post("", response_model=NgduRead, status_code=201, summary="Create an NGDU")
def create_ngdu(
    payload: NgduCreate, service: NgduService = Depends(get_ngdu_service)
) -> NgduRead:
    return service.create(payload)


@router.put("/{ngdu_id}", response_model=NgduRead, summary="Rename an NGDU")
def update_ngdu(
    ngdu_id: int, payload: NgduUpdate, service: NgduService = Depends(get_ngdu_service)
) -> NgduRead:
    return service.update(ngdu_id, payload)


@router.delete("/{ngdu_id}", status_code=status.HTTP_204_NO_CONTENT, summary="Delete an NGDU")
def delete_ngdu(ngdu_id: int, service: NgduService = Depends(get_ngdu_service)) -> None:
    service.delete(ngdu_id)
