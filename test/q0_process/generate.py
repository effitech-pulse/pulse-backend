"""Переносимый q0-p-v1: только синтетические данные, identity fail-closed."""
from __future__ import annotations
import argparse
import csv
from datetime import datetime, timedelta, timezone
import hashlib
import io
import json
import logging
from pathlib import Path
import platform
import random
import shutil
import zipfile
import zlib

class DeterministicZipFile(zipfile.ZipFile):
    """Пишет исходные ZipInfo вместо текущего времени/атрибутов платформы."""

    def writestr(self, name: str, data: str | bytes, **kwargs: object) -> None:
        info = zipfile.ZipInfo(name, (2026, 10, 20, 22, 21, 50))
        info.compress_type = zipfile.ZIP_DEFLATED
        info.create_system = 3
        info.create_version = 20
        info.extract_version = 20
        info.external_attr = 25165824
        super().writestr(info, data, compress_type=zipfile.ZIP_DEFLATED, compresslevel=6)

def generate(root: Path, *, only_w1: bool=False) -> None:
    """Создаёт новый namespace; при расхождении SHA оставляет его для диагностики."""
    if not only_w1 and (platform.python_version(), zlib.ZLIB_RUNTIME_VERSION) != ('3.13.5', '1.2.12'):
        raise RuntimeError('Exact generation requires CPython 3.13.5 / zlib runtime 1.2.12')
    fixtures = Path(__file__).resolve().parent / 'fixtures'
    root.mkdir(parents=True, exist_ok=False)
    ds = root / 'datasets'
    ds.mkdir()
    for name in ('registry',):
        shutil.copytree(fixtures / name, root / name)
    for name in ('pin.json',):
        shutil.copyfile(fixtures / name, root / name)
    for name in ('evidence', 'parser-temp', 'ml-temp'):
        (root / name).mkdir()
    shutil.copyfile(Path(__file__).with_name('ml_launcher.py'), root / 'ml_launcher.py')
    shutil.copyfile(Path(__file__).with_name('q0_synthetic.py'), root / 'q0_synthetic.py')
    channels = ['F_out', 'Ia', 'CosF', 'Ped_load', 'Pump_intake_pressure_kgs', 'Motor_winding_temperature', 'R_izol', 'qg']
    allchannels = channels + ['set_Annular_pressure'] + ['synthetic_%02d' % i for i in range(64)]

    def schema(scale: float=1) -> dict[str, object]:
        """Описывает искусственные каналы, включая отдельную уставку."""
        return {'tags': [{'alias': x, 'server_parameter': {'is_set': x.startswith('set_'), 'coef_k': scale if x.startswith('synthetic_') else 1, 'coef_b': 0}} for x in allchannels]}
    (root / 'schema.json').write_text(json.dumps(schema()))
    (root / 'amplify-schema.json').write_text(json.dumps(schema(1.2345678901234567)))
    manifest = {}

    def save(name: str, data: bytes, rows: int, cols: int, **extra: object) -> None:
        """Сохраняет bytes и вычисляет фактическую identity."""
        (ds / name).write_bytes(data)
        manifest[name] = {'raw_bytes': len(data), 'raw_sha256': 'sha256:' + hashlib.sha256(data).hexdigest(), 'rows': rows, 'columns': cols, **extra}
    start = datetime(2026, 1, 1, tzinfo=timezone.utc)

    def ts(i: int, seconds: int=3600) -> str:
        """Строит UTC timestamp без локального часового пояса."""
        return (start + timedelta(seconds=i * seconds)).strftime('%Y-%m-%dT%H:%M:%SZ')

    def w1(stopped: bool=False) -> bytes:
        """Сохраняет валидные нули и маркеры до обработки parser."""
        s = io.StringIO()
        w = csv.writer(s, lineterminator='\n')
        w.writerow(['time', 'pulse'] + channels + ['set_Annular_pressure'])
        for i in range(745):
            run = not stopped and (not 24 <= i < 28)
            w.writerow([ts(i), '9007199254740993', 50 if run else 0, 22 if run else 0, 0.87, 55, 45, 92, 9999 if i % 20 == 0 else 100, 0, 9999])
        return s.getvalue().encode()
    save('w1.csv', w1(), 745, 11)
    save('w1-insufficient.csv', w1(True), 745, 11)
    if only_w1:
        return
    rng = random.Random(20261020)
    alphabet = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
    for cols in [128, 129]:
        s = io.StringIO()
        s.write('<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>')

        def col(i: int) -> str:
            """Возвращает Excel имя колонки с единицы."""
            out = ''
            while i:
                i, r = divmod(i - 1, 26)
                out = chr(65 + r) + out
            return out
        headers = ['time'] + channels + ['unused_%03d' % i for i in range(cols - 9)]
        for row in range(746):
            s.write('<row r="%d">' % (row + 1))
            for c in range(cols):
                val = headers[c] if row == 0 else ts(row - 1) if c == 0 else str([50, 22, 0.87, 55, 45, 92, 100, 0][c - 1]) if c <= 8 else ''.join(rng.choices(alphabet, k=70))
                s.write('<c r="%s%d" t="inlineStr"><is><t>%s</t></is></c>' % (col(c + 1), row + 1, val))
            s.write('</row>')
        s.write('</sheetData></worksheet>')
        worksheet = s.getvalue().encode()
        buf = io.BytesIO()
        with DeterministicZipFile(buf, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as z:
            z.writestr('[Content_Types].xml', '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>')
            z.writestr('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>')
            z.writestr('xl/workbook.xml', '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Telemetry" sheetId="1" r:id="rId1"/></sheets></workbook>')
            z.writestr('xl/_rels/workbook.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>')
            z.writestr('xl/worksheets/sheet1.xml', worksheet)
        data = buf.getvalue()
        expanded = sum((len(x) for x in [worksheet])) + sum((i.file_size for i in zipfile.ZipFile(io.BytesIO(data)).infolist() if i.filename != 'xl/worksheets/sheet1.xml'))
        assert len(worksheet) > 8 << 20 and expanded < 100 << 20 and (expanded / len(data) < 100)
        save('w2-%d.xlsx' % cols, data, 745, cols, expanded_bytes=expanded, worksheet_bytes=len(worksheet), expansion_ratio=expanded / len(data))
    head = 'time,' + ','.join(('synthetic_%02d' % i for i in range(64))) + ',unused\n'
    base = [ts(i, 30) + ',' + ','.join(['1'] * 64) + ',' for i in range(100000)]
    compact = '\n'.join(base) + '\n'
    target = 20 << 20
    remaining = target - len(head.encode()) - len(compact.encode())
    q, r = divmod(remaining, 100000)
    assert 0 < q < 65536
    raw = head + ''.join((row + 'x' * (q + (i < r)) + '\n' for i, row in enumerate(base)))
    assert len(raw.encode()) == target
    save('w3.csv', raw.encode(), 100000, 66)
    save('w3-over-raw.csv', raw.encode() + b'\n', 100000, 66)
    rows = head + compact + ts(100000, 30) + ',' + ','.join(['1'] * 64) + ',\n'
    assert len(rows.encode()) < target
    save('w3-over-rows.csv', rows.encode(), 100001, 66)
    save('w4.csv', (head + compact).encode(), 100000, 66)
    manifest['generator'] = {'version': 'q0-p-v1', 'seed': 20261020, 'MiB': 1024 ** 2}
    expected = json.loads((fixtures / 'manifest.json').read_text())
    if manifest != expected:
        raise ValueError('Generated dataset identities differ from retained q0-p-v1 manifest')
    (root / 'manifest.json').write_bytes((fixtures / 'manifest.json').read_bytes())
if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=Path, help='New operator-owned output directory; must not exist')
    parser.add_argument('--only-w1', action='store_true', help='Small smoke fixture only; never resource/replay acceptance')
    args = parser.parse_args()
    generate(args.output, only_w1=args.only_w1)
    logging.basicConfig(level=logging.INFO)
    logging.info('Generated %s; only_w1=%s', args.output, args.only_w1)
