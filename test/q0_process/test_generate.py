"""Малые проверки реального генератора без resource/process suite."""
from __future__ import annotations

import hashlib
import io
from pathlib import Path
import tempfile
import unittest
import zipfile


class GeneratorTests(unittest.TestCase):
    def test_w1_matches_retained_identity_and_refuses_existing_root(self) -> None:
        # Catches changed telemetry values/format and accidental overwrite of old evidence.
        import generate
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / 'fresh'
            generate.generate(root, only_w1=True)
            self.assertTrue((root / 'datasets/w1.csv').is_file(), 'generator must write W1')
            self.assertEqual(hashlib.sha256((root / 'datasets/w1.csv').read_bytes()).hexdigest(),
                             'cca0ce8f6e59758b73e6fc19a1d66c627299406379e160ba30c8defcd9de5c88')
            self.assertEqual(hashlib.sha256((root / 'datasets/w1-insufficient.csv').read_bytes()).hexdigest(),
                             '82f136ca54cef49968ad4aa98935bdf9c1b16f02f1f12499c69ca7cb5545a544')
            with self.assertRaises(FileExistsError):
                generate.generate(root, only_w1=True)

    def test_zip_writer_preserves_original_metadata(self) -> None:
        # Catches wall-clock-dependent XLSX bytes and implicit platform attributes.
        import generate
        outputs = []
        for _ in range(2):
            buf = io.BytesIO()
            with generate.DeterministicZipFile(buf, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
                archive.writestr('xl/workbook.xml', '<workbook/>')
            outputs.append(buf.getvalue())
        self.assertEqual(outputs[0], outputs[1])
        with zipfile.ZipFile(io.BytesIO(outputs[0])) as archive:
            entry = archive.infolist()[0]
            self.assertEqual(entry.date_time, (2026, 10, 20, 22, 21, 50))
            self.assertEqual((entry.create_system, entry.external_attr, entry.compress_type),
                             (3, 25165824, 8))
            self.assertEqual(archive.read(entry), b'<workbook/>')


if __name__ == '__main__':
    unittest.main()
