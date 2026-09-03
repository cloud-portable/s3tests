# htmlreport example

Runs the compatibility corpus against an S3 endpoint and writes the
[`report/html`](../../report/html) report.

```
go run ./examples/htmlreport \
  -endpoint http://127.0.0.1:9000 -access-key minioadmin -secret-key minioadmin \
  -target "MinIO (local docker)" -o report.html
```

Select a subset with `-groups` (comma-separated feature groups) or `-tags`
(vector must carry at least one, e.g. `-tags tier-1`); the filter is stamped
into the report's provenance panel.

Committed samples, generated against a disposable MinIO container
(`make samples` from `packages/go/` regenerates both):

- [`report-full.html`](report-full.html) — the whole corpus (all api groups)
- [`report-tier-1.html`](report-tier-1.html) — `-tags tier-1`
  ([Tier 1: Core](https://github.com/cloud-portable/storage/blob/main/tier-1.yaml)
  operations only)

The pass rates in these samples describe MinIO, not the runner.
