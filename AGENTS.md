# AGENTS.md

Guidance for AI coding agents working in this repository.

## What this repo is

Test **runners** for the language-independent S3 compatibility test vectors
published by [`cloud-portable/s3vectors`](https://github.com/cloud-portable/s3vectors).
A runner executes the corpus's `api`-kind vectors against an S3 endpoint and
reports one of four outcomes per vector: `pass`, `fail`, `blocked`, `skipped`.

The repo mirrors s3vectors' `packages/` convention — one self-contained
package per language:

- `packages/go` — module `github.com/cloud-portable/s3tests/packages/go`,
  package `s3tests` (programmatic library) plus the `cmd/s3tests` CLI.
- `packages/js` — npm package `@cloud-portable/s3tests` (plain ESM, no build
  step, hand-written `.d.ts`), library plus the `s3tests` bin. A feature-parity
  port of the Go runner: same config surface, outcomes, reporters and CLI
  flags (`--flag` style instead of Go's `-flag`).
- `shared/report` — the HTML report page template and cross-language golden
  file (see below). `scripts/` holds the sync script.

**The corpus is a dependency, never data in this repo.** Vectors come from
`github.com/cloud-portable/s3vectors/packages/go`; do not vendor or copy
vector JSON here. The normative spec (placeholder grammar, matcher semantics,
generated-data algorithm, outcome semantics) is the s3vectors README — link to
it, don't restate it.

## Setup gotcha

The s3vectors corpus packages aren't published yet, so both runners point at
the sibling checkout, which must exist at `~/Code/alanshaw/s3vectors`:

- `packages/go/go.mod` has a `replace` to `../../../s3vectors/packages/go`.
- `packages/js/package.json` depends on
  `"@cloud-portable/s3vectors": "file:../../../s3vectors/packages/js"` —
  `npm install` symlinks it, so corpus changes are picked up live, but a fresh
  `npm ci`/`npm install` is needed if the corpus package's own dependencies
  change.

Drop both once the s3vectors packages are public.

## Commands

From `packages/go/`:

```
make test            # go vet + full unit suite (includes offline corpus smoke test)
make integration     # curated groups against a disposable MinIO container (needs Docker)
make samples         # regenerate committed examples/htmlreport reports (needs Docker)
make golden          # regenerate shared/report/golden.html (Go is canonical)
go test ./...        # unit tests without the template sync check
```

From `packages/js/` (same target names):

```
npm install          # once; links the corpus via the file: dependency
make test            # npm test = template sync check + node --test (incl. corpus smoke + golden)
make integration     # curated groups against a disposable MinIO container (needs Docker)
make samples         # regenerate committed examples/htmlreport reports (needs Docker)
```

The integration tests also run standalone against any endpoint:
`S3TESTS_ENDPOINT=... S3TESTS_ACCESS_KEY=... S3TESTS_SECRET_KEY=... go test -tags integration -run TestIntegration .`
(Go) or `S3TESTS_ENDPOINT=... node --test test/integration.test.js` (JS).

For Go changes, always run `gofmt -w`, `go vet ./...` and `go test ./...`
before considering a change done, and compile the tagged files:
`go build -tags integration ./...`. For JS changes, `npm test` must pass.

## Shared HTML template + cross-language golden

The HTML reporters must produce **byte-identical** output across languages.
Three mechanisms enforce it — keep all three intact:

- `shared/report/page.mustache` is the canonical template (logic-less
  mustache; view models supply pre-formatted strings and explicit booleans).
  Edit only this copy, then run `node scripts/sync-report-template.js` to copy
  it into `packages/go/report/html/page.mustache` and
  `packages/js/report/page.mustache`; both test suites run the script with
  `--check` and fail on drift. Never hand-edit the package copies.
- `shared/report/fixture.json` + `shared/report/golden.html` pin rendering:
  each package's golden test renders the fixture and byte-compares. Go is
  canonical — after a template or view-model change, regenerate with
  `make golden` from `packages/go/` and re-run the JS golden test.
- View-model formatting (percentages, durations, timestamps) is integer
  arithmetic specified by the Go implementation; the JS reporter overrides
  mustache.js's HTML escaping to match Go's `template.HTMLEscapeString`
  (exactly `& ' < > "` → `&amp; &#39; &lt; &gt; &#34;`). Any new formatted
  field needs the same treatment in both packages.

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
  (`html.Write(w, results, meta)` — one self-contained dark-first dashboard
  page with a coverage-style groups summary table, inline CSS, zero
  JavaScript; output is deterministic — reporters never call time.Now(),
  timestamps come only from Meta.GeneratedAt) and
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
- `cmd/s3tests/` — the CLI: streams console progress by default; repeatable
  `--report`/`-r <format>[=<path>]` flags write file reports (formats: junit,
  html — extend the `reporters` map), selection flags mirror the filter
  funcs, static `-alt-*` flags satisfy $credential vectors, SIGINT cancels
  gracefully (teardown still runs). Exit code 1 iff any vector failed
  (blocked does not fail the run — the CLI cannot know if a second identity
  was intentionally omitted)
- `examples/htmlreport/` — runnable end-to-end example (flags for
  endpoint/groups/tags) plus committed sample reports (`report-full.html`,
  `report-tier-1.html`), regenerated against MinIO via `make samples`
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

## Architecture (packages/js)

A one-to-one port of the Go structure — when changing runner behavior, change
both packages (Go first, then mirror):

- `index.js` / `index.d.ts` — `Runner` (async-generator `run(vectors,
  {signal})` with a worker pool), `vectors()`, `applyFilters` + filter
  constructors. Types are maintained by hand; keep `index.d.ts` and the
  `report/*.d.ts` files in step with API changes
- `report/` — `junit.js`, `html.js` (mustache render of the shared template)
  and `nodetest.js` (`run(t, results)` — one `t.test` subtest per vector),
  each with a `.d.ts`; `write(w, results, meta)` accepts arrays or (async)
  iterables
- `lib/cli.js` + `bin/s3tests.js` — the CLI (same flags/exit codes as Go's,
  `--flag` style); `run(argv, stdout, stderr)` is directly testable
- `lib/` — mirrors the Go layout: `config.js` (client tuning + identity
  registry), `provision.js`, `vector.js`, `step-operation.js`,
  `step-http.js`, and ports of the internal packages: `interp.js`,
  `vdata.js`, `match.js`, `jsonpath.js`, `dispatch.js`, `coerce.js`,
  `rawhttp.js`, `presign.js`
- JS-specific mechanics worth knowing: aws-sdk-js-v3 has no runtime operation
  schemas, so `coerce.js` drives input coercion (timestamps, body streaming,
  number stringification) from explicit key sets derived from the corpus —
  the offline corpus smoke test is what keeps those sets honest; client
  tuning is `maxAttempts: 1`, `requestChecksumCalculation: 'WHEN_REQUIRED'`,
  removal of `addExpectContinueMiddleware`; raw status/headers are captured
  via a deserializer-adjacent middleware; anonymous identity strips auth
  headers after `awsAuthMiddleware`
- `examples/htmlreport/` — end-to-end example + committed sample reports,
  regenerated via `make samples`

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

- The **offline corpus smoke test** (`corpus_smoke_test.go` /
  `test/corpus-smoke.test.js`) dry-runs every api vector with no network:
  every placeholder must resolve, every operation input must decode, every
  regex must compile. Each must report **exactly one** known problem:
  `lifecycle-config-0010` uses `PutBucketLifecycle`, which both
  aws-sdk-go-v2 and `@aws-sdk/client-s3` removed (allow-listed). If a corpus
  version bump surfaces new problems, fix the runners — don't grow the
  allowlist without understanding why.
- The **MinIO integration tests** assert runner *mechanics* (no unexpected
  RunnerErrors, outcome counts sum, no leaked `s3tests-*` buckets) — the
  pass/fail counts are a statement about MinIO, not about the runner. Current
  baseline on the curated groups: Go ~326 pass / ~104 fail / 1 blocked, JS
  ~323 pass / ~111 fail / 1 blocked (small SDK-behavior deltas are expected;
  investigate only if the gap grows). Do not gate on pass rate.
  `multipart-0033` is flaky against MinIO (transient connection reset;
  retries are disabled by design).
- Unit tests are table-driven and use `httptest`/`node:http` or raw
  socket-listener fakes; no test other than the integration tests may need a
  real server.

## Known upstream corpus issues (report, don't work around)

- `lifecycle-config-0010`: uses `PutBucketLifecycle` (see above).
- `multipart-0013`: `CopySource: "${res.b1.name}/ "` — the trailing-space key
  is trimmed as HTTP optional whitespace by any server, so it cannot pass as
  written (should be `%20`). The runner sends `CopySource` verbatim by design,
  because the corpus pre-encodes elsewhere (e.g. `my-obj%3Ftest%26data`).

## Conventions

- Go ≥ 1.24, stdlib-first; current deps: aws-sdk-go-v2, smithy-go,
  cbroglie/mustache and the s3vectors corpus package. JS: Node ≥ 22,
  node-builtins-first, plain ESM with no build step; current deps:
  @aws-sdk/client-s3 (+presigner), the @smithy signing/http packages,
  @aws-crypto/sha256-js, mustache and the s3vectors corpus package. Justify
  any new dependency in either package.
- Future language runners get their own `packages/<lang>` directory with their
  own README and LICENSE.md (dual Apache-2.0/MIT, copied from the repo root),
  mirroring the s3vectors layout — and must render the shared HTML template
  byte-identically (add a golden test against `shared/report`).
- Test against disposable credentials/tenants only: the runner creates and
  deletes buckets, and some object-lock vectors create COMPLIANCE-retained
  objects that are undeletable until 2999.
