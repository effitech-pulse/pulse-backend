PY ?= .venv/bin/python

run:
	$(PY) -m uvicorn src.main:app --reload --port 8000
test:
	$(PY) -m pytest
migrate:
	$(PY) -m alembic upgrade head
