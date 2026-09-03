MINIO_NAME := s3tests-minio

.PHONY: test integration samples minio-start minio-stop

test:
	go vet ./...
	go test ./...

# Run the integration suite against a disposable MinIO container.
integration: minio-start
	S3TESTS_ENDPOINT=http://127.0.0.1:9000 go test -tags integration -run TestIntegration -v -count=1 . ; \
	status=$$? ; $(MAKE) minio-stop ; exit $$status

# Regenerate the committed sample reports in examples/htmlreport/.
samples: minio-start
	go run ./examples/htmlreport -target "MinIO (local docker)" -o examples/htmlreport/report-full.html && \
	go run ./examples/htmlreport -target "MinIO (local docker)" -tags tier-1 -o examples/htmlreport/report-tier-1.html ; \
	status=$$? ; $(MAKE) minio-stop ; exit $$status

minio-start:
	docker run -d --rm --name $(MINIO_NAME) -p 127.0.0.1:9000:9000 minio/minio server /data
	@until curl -sf http://127.0.0.1:9000/minio/health/ready > /dev/null; do sleep 0.5; done

minio-stop:
	docker stop $(MINIO_NAME) > /dev/null 2>&1 || true
