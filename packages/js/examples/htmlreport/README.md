# htmlreport example

Runs the compatibility corpus against an S3 endpoint and writes the
[`report/html`](../../report) report.

```
node examples/htmlreport/main.js \
  --endpoint http://127.0.0.1:9000 --access-key minioadmin --secret-key minioadmin \
  --target "MinIO (local docker)" -o report.html
```

Select a subset with `--groups` (comma-separated feature groups) or `--tags`
(vector must carry at least one, e.g. `--tags tier-1`); the filter is stamped
into the report's provenance panel.

## Using the CLI instead

This example exists to show the library API; if you just want a report, the
[`s3tests` CLI](../../bin/s3tests.js) does the same thing (and streams console
progress while running):

```sh
npm install -g @cloud-portable/s3tests

# equivalent of report-full.html
s3tests --endpoint http://127.0.0.1:9000 --access-key minioadmin --secret-key minioadmin \
  --target "MinIO (local docker)" --concurrency 4 -r html=report-full.html

# equivalent of report-tier-1.html
s3tests --endpoint http://127.0.0.1:9000 --access-key minioadmin --secret-key minioadmin \
  --target "MinIO (local docker)" --concurrency 4 --tags tier-1 -r html=report-tier-1.html
```

A bare `-r html` writes to `report.html`, and `--report`/`-r` is repeatable —
add `-r junit` to also emit JUnit XML for CI in the same run.

Committed samples, generated against a disposable MinIO container
(`make samples` from `packages/js/` regenerates both):

- [`report-full.html`](report-full.html) — the whole corpus (all api groups)
- [`report-tier-1.html`](report-tier-1.html) — `--tags tier-1`
  ([Tier 1: Core](https://github.com/cloud-portable/storage/blob/main/tier-1.yaml)
  operations only)

The pass rates in these samples describe MinIO, not the runner.
