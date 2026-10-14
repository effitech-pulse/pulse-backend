"""Well service."""
from __future__ import annotations

import io

from openpyxl import Workbook, load_workbook

from src.core.exceptions import ConflictError, NotFoundError, ValidationAppError
from src.models.well import Well
from src.repositories.well import WellRepository
from src.schemas.well import WellCreate, WellImportResult, WellRead, WellUpdate

__all__ = ["WellService", "parse_wells_xlsx"]

_EXPORT_HEADER = ["NGDU", "WELL_NUMBER", "ARMITS_ID", "UNITEK_ID", "CEX"]

# Excel header (case-insensitive) -> Well field. WELL_NUMBER is accepted but not
# stored (no column in the schema); CEX maps to the `department` column.
_HEADER_ALIASES = {
    "ngdu": "ngdu",
    "нгду": "ngdu",
    "well_number": "well_number",
    "well": "well_number",
    "номер скважины": "well_number",
    "armits_id": "armits_id",
    "armits": "armits_id",
    "unitek_id": "unitek_id",
    "unitek": "unitek_id",
    "cex": "department",
    "цех": "department",
    "department": "department",
}

_REQUIRED = ("unitek_id", "armits_id", "ngdu", "department")


def _to_int(value: object) -> int | None:
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return int(value) if value.is_integer() else None
    text = str(value).strip()
    if not text:
        return None
    try:
        return int(float(text))
    except ValueError:
        return None


def parse_wells_xlsx(content: bytes) -> list[dict]:
    """Parse an .xlsx into raw row dicts keyed by Well field names.

    Maps the header row by known aliases; unknown columns are ignored. Raises
    ValidationAppError if the file is unreadable or has no recognised columns.
    """
    try:
        wb = load_workbook(io.BytesIO(content), read_only=True, data_only=True)
    except Exception as exc:  # noqa: BLE001 - surface as 422
        raise ValidationAppError(f"Не удалось прочитать файл Excel: {exc}") from exc

    ws = wb.active
    rows = list(ws.iter_rows(values_only=True))
    if not rows:
        return []

    col_map: dict[str, int] = {}
    for idx, name in enumerate(rows[0]):
        if name is None:
            continue
        field = _HEADER_ALIASES.get(str(name).strip().lower())
        if field:
            col_map[field] = idx

    if not any(f in col_map for f in _REQUIRED):
        raise ValidationAppError(
            "В файле не найдены ожидаемые колонки (NGDU, ARMITS_ID, UNITEK_ID, CEX)"
        )

    out: list[dict] = []
    for raw in rows[1:]:
        if raw is None or all(c is None for c in raw):
            continue
        out.append({field: (raw[idx] if idx < len(raw) else None) for field, idx in col_map.items()})
    return out


class WellService:
    def __init__(self, repo: WellRepository) -> None:
        self._repo = repo

    def list_active(self) -> list[WellRead]:
        return [WellRead.model_validate(w) for w in self._repo.list_active()]

    def list(self, active_only: bool = True) -> list[WellRead]:
        return [WellRead.model_validate(w) for w in self._repo.list_filtered(active_only)]

    def get(self, well_id: int) -> WellRead:
        well = self._repo.get(well_id)
        if well is None:
            raise NotFoundError(f"Well {well_id} not found")
        return WellRead.model_validate(well)

    def import_from_xlsx(self, content: bytes) -> WellImportResult:
        """Quick path: parse an Excel (default headers) and bulk-create."""
        return self.import_rows(parse_wells_xlsx(content))

    def import_rows(self, rows: list[dict]) -> WellImportResult:
        """Normalize, validate and bulk-create wells from canonical-keyed rows.

        Skips duplicates (existing or repeated unitek_id/armits_id) and reports
        per-row errors. Rows are dicts with keys ngdu/armits_id/unitek_id/department
        and an optional well_number.
        """
        created = skipped = 0
        errors: list[str] = []
        seen_unitek: set[int] = set()
        seen_armits: set[int] = set()

        for offset, rec in enumerate(rows, start=2):  # row 2 = first data row
            unitek_id = _to_int(rec.get("unitek_id"))
            armits_id = _to_int(rec.get("armits_id"))
            ngdu = _to_int(rec.get("ngdu"))
            department = _to_int(rec.get("department"))

            if None in (unitek_id, armits_id, ngdu, department):
                errors.append(
                    f"строка {offset}: пропущены/некорректны обязательные поля "
                    f"(UNITEK_ID, ARMITS_ID, NGDU, CEX)"
                )
                continue

            if (
                unitek_id in seen_unitek
                or armits_id in seen_armits
                or self._repo.get_by_unitek_id(unitek_id) is not None
                or self._repo.get_by_armits_id(armits_id) is not None
            ):
                skipped += 1
                continue

            wn = rec.get("well_number")
            self._repo.add(
                Well(
                    unitek_id=unitek_id,
                    armits_id=armits_id,
                    well_number=str(wn).strip() if wn not in (None, "") else None,
                    ngdu=ngdu,
                    department=department,
                    active=True,
                )
            )
            seen_unitek.add(unitek_id)
            seen_armits.add(armits_id)
            created += 1

        if created:
            self._repo.commit()
        return WellImportResult(created=created, skipped=skipped, errors=errors)

    def create(self, data: WellCreate) -> WellRead:
        """Create one well manually; rejects duplicate unitek_id/armits_id."""
        if self._repo.get_by_unitek_id(data.unitek_id) is not None:
            raise ConflictError(f"Скважина с UNITEK_ID={data.unitek_id} уже существует")
        if self._repo.get_by_armits_id(data.armits_id) is not None:
            raise ConflictError(f"Скважина с ARMITS_ID={data.armits_id} уже существует")
        well = self._repo.add(
            Well(
                unitek_id=data.unitek_id,
                armits_id=data.armits_id,
                well_number=data.well_number,
                ngdu=data.ngdu,
                department=data.department,
                active=data.active,
            )
        )
        self._repo.commit()
        return WellRead.model_validate(well)

    def update(self, well_id: int, data: WellUpdate) -> WellRead:
        """Partial update of a well; checks uniqueness if keys change."""
        well = self._repo.get(well_id)
        if well is None:
            raise NotFoundError(f"Well {well_id} not found")
        fields = data.model_dump(exclude_unset=True)

        if "unitek_id" in fields and fields["unitek_id"] != well.unitek_id:
            if self._repo.get_by_unitek_id(fields["unitek_id"]) is not None:
                raise ConflictError(f"Скважина с UNITEK_ID={fields['unitek_id']} уже существует")
        if "armits_id" in fields and fields["armits_id"] != well.armits_id:
            if self._repo.get_by_armits_id(fields["armits_id"]) is not None:
                raise ConflictError(f"Скважина с ARMITS_ID={fields['armits_id']} уже существует")

        for key, value in fields.items():
            setattr(well, key, value)
        self._repo.commit()
        return WellRead.model_validate(well)

    def delete(self, well_id: int) -> None:
        well = self._repo.get(well_id)
        if well is None:
            raise NotFoundError(f"Well {well_id} not found")
        self._repo.delete(well)
        self._repo.commit()

    def export_xlsx(self) -> bytes:
        """Export all wells to an .xlsx with the default import headers."""
        wb = Workbook()
        ws = wb.active
        ws.title = "wells"
        ws.append(_EXPORT_HEADER)
        for w in self._repo.list():
            ws.append([w.ngdu, w.well_number, w.armits_id, w.unitek_id, w.department])
        buf = io.BytesIO()
        wb.save(buf)
        return buf.getvalue()
