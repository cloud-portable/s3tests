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
- `packages/python` — PyPI distribution `cloud-portable-s3tests`, import
  package `cloud_portable_s3tests` (src layout, hatchling, `py.typed`,
  synchronous API on boto3), library plus the `s3tests` console script.
  Feature-parity port of the JS runner: same config surface, outcomes,
  reporters (`report.junit`, `report.html`, `report.unittest`) and CLI flags.
- `shared/report` — the HTML report page template and cross-language golden
  file (see below). `scripts/` holds the sync script.

**The corpus is a dependency, never data in this repo.** Vectors come from
`github.com/cloud-portable/s3vectors/packages/go`; do not vendor or copy
vector JSON here. The normative spec (placeholder grammar, matcher semantics,
generated-data algorithm, outcome semantics) is the s3vectors README — link to
it, don't restate it.

## Setup gotcha

The s3vectors packages aren't published yet, so all three runners point at the
sibling checkout, which must exist next to this repo (`../s3vectors`):

- `packages/go/go.mod` has a `replace` of
  `github.com/cloud-portable/s3vectors/packages/go` to
  `../../../s3vectors/packages/go`.
- `packages/js/package.json` depends on
  `"@cloud-portable/s3vectors": "file:../../../s3vectors/packages/js"` —
  `npm install` symlinks it, so corpus changes are picked up live, but a fresh
  `npm ci`/`npm install` is needed if the corpus package's own dependencies
  change.
- `packages/python/Makefile`'s `make setup` creates `.venv` and installs the
  corpus editable from `../../../s3vectors/packages/python` before installing
  the runner package editable (`pyproject.toml` just names
  `cloud-portable-s3vectors`, which would otherwise resolve from PyPI).

Drop all three once the s3vectors packages are public.

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

From `packages/python/` (same target names; Node is still needed for the
template sync check):

```
make setup           # once; venv + editable corpus + editable package
make test            # sync check + python -m unittest discover -s tests (incl. corpus smoke + golden)
make integration     # curated groups against a disposable MinIO container (needs Docker)
make samples         # regenerate committed examples/htmlreport reports (needs Docker)
```

The integration tests also run standalone against any endpoint:
`S3TESTS_ENDPOINT=... S3TESTS_ACCESS_KEY=... S3TESTS_SECRET_KEY=... go test -tags integration -run TestIntegration .`
(Go), `S3TESTS_ENDPOINT=... node --test test/integration.test.js` (JS) or
`S3TESTS_ENDPOINT=... .venv/bin/python -m unittest discover -s tests -p test_integration.py`
(Python).

For Go changes, always run `gofmt -w`, `go vet ./...` and `go test ./...`
before considering a change done, and compile the tagged files:
`go build -tags integration ./...`. For JS changes, `npm test` must pass. For
Python changes, `make test` from `packages/python/` must pass.

## Shared HTML template + cross-language golden

The HTML reporters must produce **byte-identical** output across languages.
Three mechanisms enforce it — keep all three intact:

- `shared/report/page.mustache` is the canonical template (logic-less
  mustache; view models supply pre-formatted strings and explicit booleans).
  Edit only this copy, then run `node scripts/sync-report-template.js` to copy
  it into `packages/go/report/html/page.mustache`,
  `packages/js/report/page.mustache` and
  `packages/python/src/cloud_portable_s3tests/report/page.mustache`; all
  three test suites run the script with `--check` and fail on drift. Never
  hand-edit the package copies.
- `shared/report/fixture.json` + `shared/report/golden.html` pin rendering:
  each package's golden test renders the fixture and byte-compares. Go is
  canonical — after a template or view-model change, regenerate with
  `make golden` from `packages/go/` and re-run the JS and Python golden tests.
- View-model formatting (percentages, durations, timestamps) is integer
  arithmetic specified by the Go implementation; the JS reporter overrides
  mustache.js's HTML escaping to match Go's `template.HTMLEscapeString`
  (exactly `& ' < > "` → `&amp; &#39; &lt; &gt; &#34;`), and the Python
  reporter renders with its own minimal mustache (`report/_mustache.py`:
  escaped variables, `#`/`^` sections, dotted names, `{{.}}`, spec
  standalone-line stripping) using the same escape set — no Python mustache
  library allows that override. Any new formatted field needs the same
  treatment in all three packages; Python's `_timefmt.py` holds the
  integer-nanosecond formatting helpers (never `round()` or float `%.Nf`,
  which round half-even).

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
  timestamps come only from Meta.GeneratedAt; every vector card links to
  its definition in the corpus repo via a raw-file text-fragment URL built
  from group and id by plain concatenation — no percent-encoding, by design,
  so both languages emit identical bytes) and
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

## Architecture (packages/python)

A one-to-one port of the JS structure onto boto3 (change Go first, then
mirror to JS and Python):

- `src/cloud_portable_s3tests/__init__.py` — public API: `Runner`
  (generator `run(vectors, *, skip, cancel)` over a thread pool),
  `Config`/`Credential`, `vectors()`, `apply_filters` + filter constructors,
  `skip`, the result dataclasses (`VectorResult`/`StepResult`/`CheckFailure`
  with `to_dict`/`from_dict` for the camelCase JSON contract) and the
  `Provisioner` protocol. `Runner`/`Config` load lazily (boto3 import).
- `report/` — `junit.py`, `html.py` and `unittest.py` (`run(tc, results)` —
  one `tc.subTest` per vector; runner errors raise, fails `tc.fail`,
  blocked/skipped `tc.skipTest`), plus `_mustache.py` and the synced
  `page.mustache`; `write(w, results, meta)` takes a binary file object and
  any iterable (a list or the live `run()` generator) and encodes UTF-8.
- `_cli.py` + `__main__.py` + the `s3tests` console script — same flags and
  exit codes as Go/JS; `run(argv, stdout, stderr)` is directly testable.
  argparse is subclassed so usage errors print `error: …` + the USAGE text
  and exit 2 (SIGINT: first sets the cancel event, second `os._exit(130)`).
- Flat private modules mirroring `lib/*.js`: `_config.py` (client tuning +
  identity registry), `_provision.py`, `_vector.py` (+ `_run.py` for the
  shared per-vector state), `_step_operation.py`, `_step_http.py`,
  `_interp.py`, `_vdata.py`, `_match.py`, `_jsonpath.py`, `_dispatch.py`,
  `_coerce.py`, `_rawhttp.py`, `_presign.py`, `_skip.py`, `_filter.py`,
  `_result.py`, `_timefmt.py`.
- Python-specific mechanics worth knowing: boto3 is synchronous, so `run()`
  is a generator fed by `min(concurrency, len(vectors))` worker threads; the
  generator's `finally` sets the cancel flag and joins workers, so breaking
  out cancels outstanding work while in-flight vectors still tear down (on
  their own 120 s deadline). Cancellation is checked between prerequisites
  and steps and in the raw-socket read loop — a boto3 call in flight
  completes or times out. botocore carries the service model at runtime, so
  `_coerce.build_input` coerces by shape type (timestamp → `parse_time`,
  streaming `Body` blob → bytes held aside, `$base64`/`$data` for string
  members → base64, numbers for string members → `str`, `CopySource` dict →
  `Bucket/Key[?versionId=]` verbatim) and `supported(name)` is
  `name in service_model.operation_names` — botocore still models
  `PutBucketLifecycle`, so the smoke test's allow-list is empty and
  `lifecycle-config-0010` executes. Client tuning:
  `Config(signature_version='s3v4' | UNSIGNED, retries={'total_max_attempts': 1},
  request_checksum_calculation='when_required',
  response_checksum_validation='when_required', s3={'addressing_style':
  'path' | 'virtual'}, parameter_validation=False, connect/read_timeout,
  max_pool_connections)`; handlers unregistered per client via
  `client.meta.events.unregister`: `add_expect_header`,
  `handle_copy_source_param` (boto3 URL-quotes CopySource), `sse_md5`/
  `copy_source_sse_md5` (boto3 re-base64s the key), `validate_bucket_name`,
  `set_list_objects_encoding_type_url` + the `decode_list_object*`
  after-call decoders; a `before-call` hook strips `Expect` and marks the
  request context redirected so the region redirector never issues a second
  request; a `before-parameter-build` hook reproduces the JS SDK's SSE-C
  behaviour (32-byte base64 keys verbatim, MD5 always recomputed). Raw
  status/headers come from `ResponseMetadata` (also on `ClientError`);
  bodyless error codes `404/304/405/412` normalize to
  `NotFound/NotModified/MethodNotAllowed/PreconditionFailed`, other digit
  codes to `''`; `StreamingBody` outputs are drained into `res.body` and
  excluded from the generic value; datetimes render Go-RFC3339Nano at
  millisecond precision. `$http` uses raw `socket`/`ssl` with a stdlib SigV4
  mirroring `rawhttp.js` (Content-Length never signed).
- `examples/htmlreport/` — end-to-end example + committed sample reports,
  regenerated via `make samples`.

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
  `test/corpus-smoke.test.js` / `tests/test_corpus_smoke.py`) dry-runs every
  api vector with no network: every placeholder must resolve, every operation
  input must decode, every regex must compile. Go and JS must report
  **exactly one** known problem: `lifecycle-config-0010` uses
  `PutBucketLifecycle`, which both aws-sdk-go-v2 and `@aws-sdk/client-s3`
  removed (allow-listed); botocore still models it, so the Python allow-list
  is empty. If a corpus version bump surfaces new problems, fix the runners —
  don't grow the allowlists without understanding why.
- The **MinIO integration tests** assert runner *mechanics* (no unexpected
  RunnerErrors, outcome counts sum, no leaked `s3tests-*` buckets) — the
  pass/fail counts are a statement about MinIO, not about the runner. Current
  baseline on the curated groups: Go ~326 pass / ~104 fail / 1 blocked, JS
  ~323 pass / ~111 fail / 1 blocked, Python ~326 pass / ~108 fail / 1 blocked
  (small SDK-behavior deltas are expected; investigate only if the gap
  grows). Do not gate on pass rate.
  `multipart-0033` is flaky against MinIO (transient connection reset;
  retries are disabled by design).
- Unit tests are table-driven and use `httptest`/`node:http`/
  `http.server` or raw socket-listener fakes; no test other than the
  integration tests may need a real server. The Python suite is `unittest`
  (stdlib; no pytest) and mirrors the JS test titles one-to-one.

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
  @aws-crypto/sha256-js, mustache and the s3vectors corpus package. Python:
  ≥ 3.10, stdlib-first, `unittest`; current deps: boto3 (≥ 1.36 for the
  checksum config options) and the s3vectors corpus package — no mustache
  library (hand-written renderer, see above). Justify any new dependency in
  any package.
- Future language runners get their own `packages/<lang>` directory with their
  own README and LICENSE.md (dual Apache-2.0/MIT, copied from the repo root),
  mirroring the s3vectors layout — and must render the shared HTML template
  byte-identically (add a golden test against `shared/report`).
- Test against disposable credentials/tenants only: the runner creates and
  deletes buckets, and some object-lock vectors create COMPLIANCE-retained
  objects that are undeletable until 2999.
