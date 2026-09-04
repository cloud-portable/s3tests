# cloud-portable-s3tests

A runner for the language-independent
[S3 compatibility test vectors](https://github.com/cloud-portable/s3vectors).
It executes every `api`-kind vector against an S3 endpoint — provisioning
prerequisites, interpolating placeholders, dispatching operations through
`boto3`, sending raw wire-level requests for `$http` steps, minting presigned
URLs — and evaluates the corpus's expectation matchers, reporting one of four
outcomes per vector: `pass`, `fail`, `blocked` or `skipped`.

Requires Python 3.10+. Fully typed (`py.typed`), synchronous API; the only
runtime dependencies are `boto3` and the corpus package
`cloud-portable-s3vectors`.

## Usage

### CLI

```sh
pip install cloud-portable-s3tests

s3tests --endpoint http://127.0.0.1:9000 --access-key AK --secret-key SK \
  --tags tier-1 -r junit -r html=minio.html
```

By default results just stream to the console (one line per vector, colored
on a TTY); each `--report`/`-r` flag (repeatable) also writes a file report —
give a bare format for its default path (`junit` → `report.xml`, `html` →
`report.html`) or `<format>=<path>` to choose one. Supply
`--alt-access-key`/`--alt-secret-key` (plus `--alt-canonical-id`/
`--alt-display-name` for ACL vectors) to run the `$credential` vectors —
without them those vectors report `blocked`. The exit code is 1 when any
vector failed; blocked and skipped vectors don't affect it. Connection flags
fall back to `S3TESTS_ENDPOINT`/`S3TESTS_ACCESS_KEY`/`S3TESTS_SECRET_KEY`
(and `S3TESTS_ALT_*`) environment variables.

Select vectors with `--groups`/`--tags`/`--ids`. Two families of flags then
narrow the run, with different effects on the results:

- `--exclude-groups`/`--exclude-tags`/`--exclude-ids` **drop** matching
  vectors from the run — they are absent from results and reports.
- `--skip-groups`/`--skip-tags`/`--skip-ids` **skip** matching vectors — they
  are not run either, but each appears in results and reports with outcome
  `skipped` and the flag as its reason, so a skip-list is documented rather
  than silently dropped and reports stay comparable across runs.

```sh
# Run tier-1, skipping two vectors with known server bugs; both still show
# up as "skipped" in the console and in the JUnit report.
s3tests --endpoint http://127.0.0.1:9000 --access-key AK --secret-key SK \
  --tags tier-1 --skip-ids multipart-0013,object-crud-0017 -r junit

# Skip a whole feature group the target does not implement.
s3tests --endpoint http://127.0.0.1:9000 --access-key AK --secret-key SK \
  --skip-groups acl,cors
```

All selection, exclude and skip flag values are stamped into report
provenance. `python -m cloud_portable_s3tests` runs the same CLI.

### Programmatic

```sh
pip install cloud-portable-s3tests
```

```python
from cloud_portable_s3tests import (
    Runner, Config, Credential, vectors, apply_filters, groups, tags, exclude_ids,
)

runner = Runner(Config(
    endpoint="http://127.0.0.1:9000",
    credentials=Credential("YOUR_ACCESS_KEY_ID", "YOUR_SECRET_ACCESS_KEY"),
))

# Select what to run: filters are plain predicates ANDed together by
# apply_filters, so custom selections compose with the built-ins.
selected = apply_filters(vectors(),               # the whole corpus, in manifest order
    groups("object-crud", "multipart"),
    tags("tier-1"),                               # vector has ≥1 listed tag
    exclude_ids("multipart-0013"),                # dropped: leaves no trace in results
    lambda v: len(v["steps"]) < 20,               # custom
)

# run() executes exactly the vectors given, yielding one VectorResult per
# vector as it completes. Breaking out of the loop, or setting the cancel
# event, cancels the rest of the run; in-flight vectors still tear down.
# (run() also takes skip rules — see "Skipping vectors" below.)
counts = {"pass": 0, "fail": 0, "blocked": 0, "skipped": 0}
failures = []
for v in runner.run(selected):
    print(f"{v.outcome:<8} {v.id}")
    counts[v.outcome] += 1
    if v.outcome == "fail":
        failures.append(v)

print(f"corpus {runner.corpus_version()}: {counts['pass']} pass, {counts['fail']} fail, "
      f"{counts['blocked']} blocked, {counts['skipped']} skipped")
for v in failures:
    step = v.steps[-1]
    print(f"{v.id} step {len(v.steps)} ({step.name}):")
    for f in step.failures:
        print(f"  {f.field}: expected {f.expected}, got {f.actual}")
```

Vectors are the corpus package's own dicts (`cloud_portable_s3vectors`
TypedDicts) and are shared and cached — treat them as read-only. Results are
dataclasses (`VectorResult`, `StepResult`, `CheckFailure`) with
`to_dict()`/`from_dict()` implementing the cross-runner JSON contract
(camelCase keys, integer-nanosecond durations).

#### Skipping vectors

Filtering with `apply_filters` *drops* vectors: they never reach `run()` and
leave no trace in the results. When you want a vector recorded but not
executed — a known server bug, a feature the target doesn't implement —
pass skip rules in `run()`'s `skip` argument instead. Skipped vectors are
never sent to the server but still yield a `VectorResult` in their normal
position, carrying the vector's id, group, title and tags, with
`outcome == "skipped"`, your reason in `reason`, no steps and zero duration.
Every reporter renders them, so runs stay comparable and the skip-list is
visible in the report.

`skip(reason, *filters)` builds a rule from a reason plus the same filter
predicates as `apply_filters` (ANDed, so `skip(reason)` with no filters skips
everything — a dry run listing the selection). Rules are consulted in order;
the first one matching a vector supplies its reason.

```python
from cloud_portable_s3tests import skip, groups, tags, ids

for v in runner.run(selected, skip=[
    skip("ACLs not implemented by target", groups("acl")),
    skip("tier-3 multipart too slow here", groups("multipart"), tags("tier-3")),
    skip("tracked in issue #123", ids("object-crud-0017", "copy-0004")),
]):
    if v.outcome == "skipped":
        print(f"skipped {v.id}: {v.reason}")
```

A rule is just a callable `(vector) -> str | None` — a string (even an empty
one) is the reason to skip, `None` lets the vector run — so for reasons that
vary per vector write one by hand, e.g. a skip-list mapping ids to the issue
tracking each one:

```python
known = {
    "multipart-0013": "https://example.com/issues/123",
    "object-crud-0017": "https://example.com/issues/456",
}
for v in runner.run(selected, skip=[lambda v: known.get(v["id"])]):
    ...
```

Test against real credentials for a *disposable* test account/tenant — the
runner creates and deletes buckets and objects, and a handful of object-lock
vectors create COMPLIANCE-retained objects whose buckets **cannot be deleted
until 2999** (they surface in `VectorResult.warnings`; the `bucket_prefix`
makes them identifiable). Never point it at an account holding data you care
about.

## Prerequisites and identities

- `$bucket` / `$object` prerequisites are provisioned by `Config.provisioner`
  — the built-in `default_provisioner` uses the endpoint itself
  (`CreateBucket` / `PutObject`); supply your own object implementing the
  `Provisioner` protocol to provision out-of-band. Teardown (emptying and
  deleting each vector's buckets) is part of the same interface and always
  best-effort: problems become `VectorResult.warnings`, never failures.
- `$credential` prerequisites need a second identity, which is
  server-specific: supply `Config.provision_credential`, a callable from the
  credential handle to a `Credential` (with `canonical_id`/`display_name` for
  ACL vectors). Without it, the ~56 vectors requiring one report `blocked`
  (not `fail`), per the corpus's outcome semantics.
- Step identities `main`, `anonymous` (unsigned) and `invalid` (well-formed
  signature, unknown key) are handled internally.

## Outcomes and reporting

Outcome semantics follow the corpus spec: a failed *prerequisite* is
`blocked` and a violated *expectation* is `fail`. `skipped` marks a vector
deliberately not executed: `run()` produces it for vectors matched by a skip
rule (with the rule's reason), and consumers may also synthesize it for
vectors they filtered out before the run — either way reports stay comparable
across differently-selected runs. `VectorResult` carries the stable vector
id, group, tags and per-step expected-vs-actual detail, and
`runner.corpus_version()` reports the corpus snapshot — everything needed to
emit JUnit XML/CTRF/TAP as recommended in the corpus's
[reporting guide](https://github.com/cloud-portable/s3vectors/blob/main/docs/reporting.md).

`VectorResult.runner_error` distinguishes "the runner could not execute this
vector" (unresolvable placeholder, unsupported operation) from a genuine
compatibility failure; such vectors still count as `fail`, but reports can
label them differently.

### Report formats

Each reporter is its own module — `report.junit`, `report.html` and
`report.unittest` today, CTRF and TAP planned. The `write` functions take a
binary file object and any iterable of results, including the generator that
`run()` returns, so a run can stream straight into a report file:

```python
from cloud_portable_s3tests.report import Meta, junit

with open("results.xml", "wb") as f:
    junit.write(f, runner.run(selected), Meta(
        corpus_version=runner.corpus_version(),
        target="MinIO RELEASE.2026-07-01",
    ))
```

(Or collect into a list first to format the same run multiple ways.) For
human inspection, `report.html` renders the same inputs as a single
self-contained HTML page — a dark-first dashboard (light theme via
`prefers-color-scheme`) with an overall outcome badge, a coverage-style
summary table of groups (pass-fraction bars, watermarked percentages), and
per-vector cards that open to expected-vs-actual detail and link to the
vector's definition in the corpus repo; groups with failures start expanded —
with inline CSS, no JavaScript, and deterministic output. The page template is
shared with the Go and JS runners
([`shared/report/page.mustache`](../../shared/report/page.mustache)) and all
implementations produce **byte-identical** HTML for the same results — a
golden test in each package pins that.

```python
from datetime import datetime, timezone
from cloud_portable_s3tests.report import Meta, html

with open("report.html", "wb") as f:
    html.write(f, runner.run(selected), Meta(
        corpus_version=runner.corpus_version(),
        target="MinIO RELEASE.2026-07-01",
        generated_at=datetime.now(timezone.utc),
    ))
```

A runnable end-to-end example (endpoint/filter flags, used to produce the
committed sample reports for the full corpus and for `tier-1`) lives in
[`examples/htmlreport`](examples/htmlreport).

The JUnit mapping follows the reporting guide: one `<testcase>` per vector
(`classname` = group), `blocked` → `<skipped message="blocked: ...">`, corpus
version and target as suite `<properties>`, vector tags as a per-case
property. Vectors with a `runner_error` become JUnit `<error>` elements (test
could not run), distinct from `<failure>` (expectation violated).
`Meta.omit_skipped` drops filter-skipped vectors from the report.

### Running under `unittest`

The `report.unittest` module reports each vector as a `subTest` of a running
`unittest.TestCase`, so the stdlib test runner is the text reporter — and
`python -m unittest`-based CI comes for free:

```python
import unittest
from cloud_portable_s3tests import Runner, vectors, apply_filters, groups
from cloud_portable_s3tests.report import unittest as s3unittest

class TestS3Compat(unittest.TestCase):
    def test_object_crud(self):
        runner = Runner(config)
        s3unittest.run(self, runner.run(apply_filters(vectors(), groups("object-crud"))), log=print)
```

Passing vectors log their title and real duration through the optional `log`
callable, failures call `self.fail` with the failing step's expected-vs-actual
detail, runner errors raise (a unittest *error*), and blocked/skipped vectors
skip their subtest with a `blocked:`/`skipped:` prefixed reason. Note
`python -m unittest -k` filters which tests are *reported*, not which vectors
*execute* — select vectors before the run instead (`apply_filters`), or pass
skip rules to `run()` to keep known-bad vectors visible as skipped subtests.

## Client behavior

The runner's boto3 clients are tuned so the vectors own their wire bytes:
path-style addressing (default; `Config.virtual_host_style` switches), no
retries, no implicit request checksums or response checksum validation, no
`Expect: 100-continue`, and botocore's parameter validation off. botocore's
S3 customizations that rewrite requests are switched off too — URL-quoting
of `CopySource`, re-encoding of SSE-C keys, `EncodingType=url` injection on
listings, bucket-name validation, region redirects — so what a vector says
goes on the wire, as in the Go and JS runners. `$http` steps are sent over
raw sockets (the wire-header vectors send headers Python's HTTP clients refuse
to emit) and SigV4-signed with a stdlib implementation without path
normalization.

## Known limitations

- Cancellation is checked between prerequisites and steps: a boto3 call
  already in flight completes or times out (`Config.request_timeout_ms`)
  before the vector stops. Teardown always runs, on its own two-minute
  deadline.
- botocore synthesizes error codes for bodyless responses (HEAD 404s, 304s)
  from the status; the runner maps them to the names the Go and JS SDKs
  surface (`NotFound`, `NotModified`, `MethodNotAllowed`,
  `PreconditionFailed`) and additionally matches via a small status→code map.
- Presigned URLs are executed with `http.client`; boto3 hoists some headers
  into query parameters, matching the Go and JS runners' behavior.
- `$matches` patterns are, per the spec, a portable subset valid in both
  ECMA-262 and RE2 (no lookarounds/backreferences), so they run on the stdlib
  `re` module.
- Multi-valued response headers are matched against their first value.
- The pure-Python CRC-32C and CRC-64/NVME derived fields are slow on
  multi-megabyte datasets (a few seconds each in the offline smoke test).
- `signing`-kind vectors (offline SigV4 algorithm tests) are out of scope for
  this runner and never loaded.

## Development

```
make setup        # venv with the sibling corpus checkout and this package, both editable
make test         # template sync check + unit tests + offline all-corpus smoke test + golden
make integration  # runs curated groups against a disposable MinIO container
make samples      # regenerates examples/htmlreport/*.html against MinIO
```

`make test` also verifies the shared HTML template is in sync
(`scripts/sync-report-template.js --check`, which needs Node) and
byte-compares the HTML reporter's output against the cross-language golden
file in [`shared/report`](../../shared/report).

## License

Apache-2.0 OR MIT
