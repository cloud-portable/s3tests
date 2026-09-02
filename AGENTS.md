# AGENTS.md

Guidance for AI coding agents working in this repository.

## What this repo is

Test **runners** for the language-independent S3 compatibility test vectors
published by [`cloud-portable/s3vectors`](https://github.com/cloud-portable/s3vectors).
A runner executes the corpus's `api`-kind vectors against an S3 endpoint and
reports one of four outcomes per vector: `pass`, `fail`, `blocked`, `skipped`.

The repo mirrors s3vectors' `packages/` convention — one self-contained
package per language. Only the Go runner exists today:

- `packages/go` — module `github.com/cloud-portable/s3tests/packages/go`,
  package `s3tests`. A programmatic library (no CLI yet).

**The corpus is a dependency, never data in this repo.** Vectors come from
`github.com/cloud-portable/s3vectors/packages/go`; do not vendor or copy
vector JSON here. The normative spec (placeholder grammar, matcher semantics,
generated-data algorithm, outcome semantics) is the s3vectors README — link to
it, don't restate it.

## Setup gotcha

`packages/go/go.mod` has a `replace` pointing the s3vectors dependency at the
sibling checkout `../../../s3vectors/packages/go` (the module isn't published
yet). Builds require that checkout to exist at
`~/Code/alanshaw/s3vectors`. Drop the replace once
`github.com/cloud-portable/s3vectors` is public.

## Commands

All from `packages/go/`:

```
make test            # go vet + full unit suite (includes offline corpus smoke test)
make integration     # curated groups against a disposable MinIO container (needs Docker)
go test ./...        # same tests as make test
```

The integration test also runs standalone against any endpoint:
`S3TESTS_ENDPOINT=... S3TESTS_ACCESS_KEY=... S3TESTS_SECRET_KEY=... go test -tags integration -run TestIntegration .`

Always run `gofmt -w`, `go vet ./...` and `go test ./...` before considering a
change done. Also compile the tagged files: `go build -tags integration ./...`.

## Architecture (packages/go)

Public surface is the root package plus the `report` subpackage tree; each
hard mechanism is an independently-tested `internal/` package:

- `s3tests.go` — `Runner`, `New`, `Vectors() []*s3vectors.Vector` (the api
  corpus, flattened), `Run(ctx, []*s3vectors.Vector) iter.Seq[VectorResult]`
  (executes exactly the vectors given, streaming results in completion order;
  breaking the loop cancels the run but in-flight teardown still completes).
  Each corpus vector carries its feature `group` (formerly the file-level
  "area")
- `report/` — public reporters over the results seq per the corpus reporting
  guide. The parent package holds only the shared `report.Meta`; each
  reporter is a subpackage: `report/junit`
  (`junit.Write(w, results, report.Meta{...})`), `report/html`
  (`html.Write(w, results, meta)` — one self-contained istanbul/c8-style page,
  inline CSS, zero JavaScript, deterministic output with no timestamps) and
  `report/gotest`
  (`gotest.Run(t, results)` — one `t.Run` subtest per vector: blocked/skipped
  → `t.Skipf` with prefixed reason, RunnerError →
  `t.Fatalf("runner error: ...")`), with CTRF and TAP planned. Mapping
  invariants: `blocked` → `<skipped message="blocked: ...">` never
  `<failure>`; `RunnerError` fails → `<error>`, not `<failure>`. Notes:
  in-package tests of the root package cannot import the report tree (import
  cycle) — tests needing both live in `package s3tests_test` (see
  `integration_test.go`); `gotest.Run` is deliberately not used by
  `integration_test.go`, which treats target compat failures as data, not
  test failures
- `config.go` — `Config` + per-identity S3 client registry (`main`,
  `anonymous`, `invalid`, `$credential` handles)
- `filter.go` — selection as composable functions: `FilterFunc`,
  `ApplyFilters(vectors, filters...)` (logical AND) and the built-in
  `Groups`/`Tags`/`IDs` + `Exclude*` constructors. Selection happens *before*
  `Run`, so the runner never emits `skipped` itself — that outcome exists for
  consumers that synthesize entries for vectors they excluded
- `result.go` — the result model (Outcome, VectorResult, StepResult)
- `provision.go` — `Provisioner` interface, `DefaultProvisioner`, teardown
- `vector.go` — per-vector executor: prerequisites → steps → teardown
- `step_operation.go` / `step_http.go` — step execution + expectation evaluation
- `internal/interp` — `${env|res|cap|data.*}` placeholder interpolation
  (`$${` escapes; unresolvable placeholder = hard error)
- `internal/vdata` — per-vector dataset cache + derived digest fields
- `internal/match` — matcher engine (subset objects, ordered arrays,
  `$exists/$absent/$eq/$matches/$length/$contains`, body digests)
- `internal/jsonpath` — capture-path grammar (`Contents[0].Key`)
- `internal/dispatch` — reflection dispatch onto `*s3.Client`: decodes vector
  params (AWS API-model member names) into SDK input structs, walks outputs
  into generic values, captures raw status/headers via middleware, maps errors
- `internal/rawhttp` — `$http` steps over raw TCP sockets + SigV4

## Invariants — do not break these

- **`blocked` ≠ `fail`**: a prerequisite that can't be established blocks the
  vector; only violated step expectations fail it. Never map one to the other.
- **`RunnerError`** marks "the runner couldn't execute this vector"
  (unsupported op, unresolvable placeholder) — outcome stays `fail`, but the
  field must be set so reports can distinguish it from a real compat failure.
- **Corpus vectors are shared and cached** by the s3vectors package: never
  mutate them. Interpolation always produces new values (JSON round-trip).
- **Vectors own their wire bytes**: SDK clients are built with retries OFF,
  implicit request checksums OFF, response checksum validation OFF, and
  `Expect: 100-continue` disabled. Don't "fix" transient failures by enabling
  retries.
- **`$http` must stay on raw sockets** — wire-header vectors send headers
  `net/http` refuses to emit (`content-length: "-1"`, empty `authorization`).
  Content-Length is deliberately excluded from SigV4 signing so vectors can
  override it on signed requests.
- **`$matches` runs on stdlib `regexp`** — the spec restricts patterns to the
  portable ECMA-262 ∩ RE2 subset (no lookarounds/backreferences); ETag
  inequality is expressed with the `$ne` operator, not lookahead regexes.
- **Teardown is best-effort and thorough**: it must cover prerequisite buckets,
  step-created buckets (`CreateBucket` ops and raw `PUT /<bucket>`), and
  explicitly delete known-written keys (`BucketInfo.KnownKeys`) because some
  servers' listings hide keys (MinIO hides `foo/bar` when object `foo`
  exists). Teardown problems become `Warnings`, never failures.
- **`signing`-kind vectors are out of scope** — never load or execute them.

## Testing rules

- The **offline corpus smoke test** (`corpus_smoke_test.go`) dry-runs all
  ~1160 api vectors with no network: every placeholder must resolve, every
  operation input must decode, every regex must compile. It must report
  **exactly one** known problem: `lifecycle-config-0010` uses
  `PutBucketLifecycle`, which aws-sdk-go-v2 removed (allow-listed). If a
  corpus version bump surfaces new problems, fix the runner — don't grow the
  allowlist without understanding why.
- The **MinIO integration test** asserts runner *mechanics* (no unexpected
  RunnerErrors, outcome counts sum, no leaked `s3tests-*` buckets) — the
  pass/fail counts are a statement about MinIO, not about the runner. Current
  baseline: ~326 pass / ~104 fail / 1 blocked. Do not gate on pass rate.
  `multipart-0033` is flaky against MinIO (transient connection reset;
  retries are disabled by design).
- Unit tests are table-driven and use `httptest` or raw `net.Listen` fakes;
  no test other than `TestIntegration` may need a real server.

## Known upstream corpus issues (report, don't work around)

- `lifecycle-config-0010`: uses `PutBucketLifecycle` (see above).
- `multipart-0013`: `CopySource: "${res.b1.name}/ "` — the trailing-space key
  is trimmed as HTTP optional whitespace by any server, so it cannot pass as
  written (should be `%20`). The runner sends `CopySource` verbatim by design,
  because the corpus pre-encodes elsewhere (e.g. `my-obj%3Ftest%26data`).

## Conventions

- Go ≥ 1.24, stdlib-first; current deps: aws-sdk-go-v2, smithy-go, and the
  s3vectors corpus package. Justify any new one.
- Future language runners get their own `packages/<lang>` directory with their
  own README and LICENSE.md (dual Apache-2.0/MIT, copied from the repo root),
  mirroring the s3vectors layout.
- Test against disposable credentials/tenants only: the runner creates and
  deletes buckets, and some object-lock vectors create COMPLIANCE-retained
  objects that are undeletable until 2999.
