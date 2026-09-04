MINIO_NAME := s3tests-minio
VENV := .venv
PY := $(VENV)/bin/python

.PHONY: setup test integration samples minio-start minio-stop

# One-time: a virtualenv with the corpus from the sibling checkout (not
# published yet), then this package, both editable.
setup:
	python3 -m venv $(VENV)
	$(PY) -m pip install --quiet --upgrade pip
	$(PY) -m pip install --quiet -e ../../../s3vectors/packages/python
	$(PY) -m pip install --quiet -e .

# Template sync check + the full unit suite (incl. corpus smoke + golden).
test:
	node ../../scripts/sync-report-template.js --check
	$(PY) -m unittest discover -s tests

# Run the integration suite against a disposable MinIO container.
integration: minio-start
	S3TESTS_ENDPOINT=http://127.0.0.1:9000 $(PY) -m unittest discover -s tests -p 'test_integration.py' -v ; \
	status=$$? ; $(MAKE) minio-stop ; exit $$status

# Regenerate the committed sample reports in examples/htmlreport/.
samples: minio-start
	$(PY) examples/htmlreport/main.py --target "MinIO (local docker)" -o examples/htmlreport/report-full.html && \
	$(PY) examples/htmlreport/main.py --target "MinIO (local docker)" --tags tier-1 -o examples/htmlreport/report-tier-1.html ; \
	status=$$? ; $(MAKE) minio-stop ; exit $$status

minio-start:
	docker run -d --rm --name $(MINIO_NAME) -p 127.0.0.1:9000:9000 minio/minio server /data
	@until curl -sf http://127.0.0.1:9000/minio/health/ready > /dev/null; do sleep 0.5; done

minio-stop:
	docker stop $(MINIO_NAME) > /dev/null 2>&1 || true
