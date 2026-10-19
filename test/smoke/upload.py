"""Проверяет настоящий Upload в isolated Compose без research cache/PG.

Запуск внутри ML-контейнера: python - < test/smoke/upload.py.
Вход только синтетический; результаты не являются оценкой качества модели.
"""
from __future__ import annotations

import csv
from datetime import datetime, timedelta, timezone
import hashlib
import io
import json
import os
import time
from urllib.parse import urlencode
from urllib.request import urlopen

import grpc
from google.protobuf.empty_pb2 import Empty
from pulse.ml.v1 import ml_pb2, ml_pb2_grpc


def main() -> None:
    """Проверяет ключ, причинную границу входа и три одинаковых Upload."""
    key = os.environ["ML_SERVICE_API_KEY"]
    channel = grpc.insecure_channel("127.0.0.1:50051")
    client = ml_pb2_grpc.MLServiceStub(channel)
    try:
        client.Health(Empty(), timeout=5)
    except grpc.RpcError as exc:
        assert exc.code() == grpc.StatusCode.UNAUTHENTICATED, exc.code()
    else:
        raise AssertionError("Health without key must fail")
    metadata = (("x-ml-api-key", key),)
    assert client.Health(Empty(), metadata=metadata, timeout=5).status == "ok"

    as_of = datetime.now(timezone.utc).replace(microsecond=0)
    query = urlencode({"well_id": "-900000", "start": (as_of - timedelta(days=60)).isoformat(), "end": as_of.isoformat()})
    with urlopen("http://data:8092/v1/telemetry?" + query, timeout=30) as response:
        telemetry = json.load(response)
    assert telemetry["points"] and not telemetry["truncated"]
    assert all(datetime.fromisoformat(point["timestamp"].replace("Z", "+00:00")) <= as_of for point in telemetry["points"])
    columns = sorted({key for point in telemetry["points"] for key in point["values"]})
    buffer = io.StringIO(newline="")
    writer = csv.writer(buffer, lineterminator="\n")
    writer.writerow(["timestamp", *columns])
    for point in telemetry["points"]:
        writer.writerow([point["timestamp"], *(point["values"].get(key) for key in columns)])
    content = buffer.getvalue().encode()
    digest = hashlib.sha256(content).hexdigest()
    samples = []
    reference = None
    for repetition in range(3):
        started = time.monotonic()
        result = client.Upload(iter([ml_pb2.UploadChunk(filename="synthetic.csv", content=content)]), metadata=metadata, timeout=300)
        elapsed = time.monotonic() - started
        assert result.n_windows > 0 and result.timeline
        assert result.provenance.input_sha256 == "sha256:" + digest
        assert result.provenance.provenance_status == "partial"
        assert result.decision_rule.policy_id == "legacy_upload_v0"
        stable = (result.verdict, result.latest_risk, result.peak_risk, result.windows_above_watch, result.windows_above_alert, result.model_version, result.decision_rule.SerializeToString(deterministic=True), tuple(point.SerializeToString(deterministic=True) for point in result.timeline))
        if reference is None:
            reference = stable
        assert stable == reference, "Identical input/bundle/rule changed its decision"
        samples.append({"phase": "first" if repetition == 0 else "repeat", "seconds": round(elapsed, 3), "verdict": result.verdict, "windows": result.n_windows})
    channel.close()
    print(json.dumps({"status": "PASS", "fixture": "synthetic", "as_of": as_of.isoformat(), "rows": len(telemetry["points"]), "channels": len(columns), "input_bytes": len(content), "input_sha256": digest, "samples": samples}, ensure_ascii=False))


if __name__ == "__main__":
    main()
