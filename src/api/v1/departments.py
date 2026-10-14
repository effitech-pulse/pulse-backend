"""Department reference endpoints (id = CEX shop code)."""
from __future__ import annotations

from fastapi import APIRouter, Depends, status

from src.core.deps import get_department_service
from src.schemas.department import DepartmentCreate, DepartmentRead, DepartmentUpdate
from src.services.department import DepartmentService

router = APIRouter(prefix="/departments", tags=["departments"])


@router.get("", response_model=list[DepartmentRead], summary="List departments")
def list_departments(
    service: DepartmentService = Depends(get_department_service),
) -> list[DepartmentRead]:
    return service.list()


@router.post("", response_model=DepartmentRead, status_code=201, summary="Create a department")
def create_department(
    payload: DepartmentCreate,
    service: DepartmentService = Depends(get_department_service),
) -> DepartmentRead:
    return service.create(payload)


@router.put("/{dep_id}", response_model=DepartmentRead, summary="Rename a department")
def update_department(
    dep_id: int,
    payload: DepartmentUpdate,
    service: DepartmentService = Depends(get_department_service),
) -> DepartmentRead:
    return service.update(dep_id, payload)


@router.delete("/{dep_id}", status_code=status.HTTP_204_NO_CONTENT, summary="Delete a department")
def delete_department(
    dep_id: int,
    service: DepartmentService = Depends(get_department_service),
) -> None:
    service.delete(dep_id)
