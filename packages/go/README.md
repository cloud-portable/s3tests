# s3tests

A runner for the language-independent
[S3 compatibility test vectors](https://github.com/cloud-portable/s3vectors).
It executes every `api`-kind vector against an S3 endpoint — provisioning
prerequisites, interpolating placeholders, dispatching operations through
`aws-sdk-go-v2`, sending raw wire-level requests for `$http` steps, minting
presigned URLs — and evaluates the corpus's expectation matchers, reporting
one of four outcomes per vector: `pass`, `fail`, `blocked` or `skipped`.

## Usage

### CLI

```sh
go install github.com/cloud-portable/s3tests/packages/go/cmd/s3tests@latest

s3tests -endpoint http://127.0.0.1:9000 -access-key AK -secret-key SK \
  -tags tier-1 -r junit -r html=minio.html
```

By default results just stream to the console (one line per vector, colored
on a TTY); each `--report`/`-r` flag (repeatable) also writes a file report —
give a bare format for its default path (`junit` → `report.xml`, `html` →
`report.html`) or `<format>=<path>` to choose one. Supply
`-alt-access-key`/`-alt-secret-key` (plus `-alt-canonical-id`/
`-alt-display-name` for ACL vectors) to run the `$credential` vectors —
without them those vectors report `blocked`. The exit code is 1 when any
vector failed; blocked and skipped vectors don't affect it. Connection flags
fall back to `S3TESTS_ENDPOINT`/`S3TESTS_ACCESS_KEY`/`S3TESTS_SECRET_KEY`
(and `S3TESTS_ALT_*`) environment variables.

Select vectors with `-groups`/`-tags`/`-ids`. Two families of flags then
narrow the run, with different effects on the results:

- `-exclude-groups`/`-exclude-tags`/`-exclude-ids` **drop** matching vectors
  from the run — they are absent from results and reports.
- `-skip-groups`/`-skip-tags`/`-skip-ids` **skip** matching vectors — they
  are not run either, but each appears in results and reports with outcome
  `skipped` and the flag as its reason, so a skip-list is documented rather
  than silently dropped and reports stay comparable across runs.

```sh
# Run tier-1, skipping two vectors with known server bugs; both still show
# up as "skipped" in the console and in the JUnit report.
s3tests -endpoint http://127.0.0.1:9000 -access-key AK -secret-key SK \
  -tags tier-1 -skip-ids multipart-0013,object-crud-0017 -r junit

# Skip a whole feature group the target does not implement.
s3tests -endpoint http://127.0.0.1:9000 -access-key AK -secret-key SK \
  -skip-groups acl,cors
```

All selection, exclude and skip flag values are stamped into report
provenance.

### Programmatic

```sh
go get github.com/cloud-portable/s3tests/packages/go
```

```go
import (
    "github.com/aws/aws-sdk-go-v2/credentials"
    s3tests "github.com/cloud-portable/s3tests/packages/go"
    s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

runner, err := s3tests.New(s3tests.Config{
    Endpoint:    "http://127.0.0.1:9000",
    Credentials: credentials.NewStaticCredentialsProvider("YOUR_ACCESS_KEY_ID", "YOUR_SECRET_ACCESS_KEY", ""),
})
if err != nil { ... }

// Select what to run: filters are plain functions ANDed together by
// ApplyFilters, so custom selections compose with the built-ins.
vectors, err := s3tests.Vectors() // the whole corpus, in manifest order
if err != nil { ... }
selected := s3tests.ApplyFilters(vectors,
    s3tests.Groups("object-crud", "multipart"),
    s3tests.Tags("tier-1"),                  // vector has ≥1 listed tag
    s3tests.ExcludeIDs("multipart-0013"),    // dropped: leaves no trace in results
    func(v *s3vectors.Vector) bool { return len(v.Steps) < 20 }, // custom
)

// Run executes exactly the vectors given, streaming one VectorResult per
// vector as it completes. Breaking out of the loop, or cancelling ctx,
// cancels the rest of the run; in-flight vectors still tear down.
// (Run also takes Skip options — see "Skipping vectors" below.)
counts := map[s3tests.Outcome]int{}
var failures []s3tests.VectorResult
for v := range runner.Run(ctx, selected) {
    fmt.Printf("%-8s %s\n", v.Outcome, v.ID)
    counts[v.Outcome]++
    if v.Outcome == s3tests.Fail {
        failures = append(failures, v)
    }
}

fmt.Printf("corpus %s: %d pass, %d fail, %d blocked, %d skipped\n",
    runner.CorpusVersion(), counts[s3tests.Pass], counts[s3tests.Fail],
    counts[s3tests.Blocked], counts[s3tests.Skipped])
for _, v := range failures {
    step := v.Steps[len(v.Steps)-1]
    fmt.Printf("%s step %d (%s):\n", v.ID, step.Index+1, step.Name)
    for _, f := range step.Failures {
        fmt.Printf("  %s: expected %s, got %s\n", f.Field, f.Expected, f.Actual)
    }
}
```

#### Skipping vectors

Filtering with `ApplyFilters` *drops* vectors: they never reach `Run` and
leave no trace in the results. When you want a vector recorded but not
executed — a known server bug, a feature the target doesn't implement —
pass `Skip` options to `Run` instead. Skipped vectors are never sent to the
server but still yield a `VectorResult` in their normal position, carrying
the vector's id, group, title and tags, with `Outcome == Skipped`, your
reason in `Reason`, no steps and zero duration. Every reporter renders them,
so runs stay comparable and the skip-list is visible in the report.

`Skip` takes a reason plus the same filter funcs as `ApplyFilters` (ANDed,
so `Skip(reason)` with no filters skips everything — a dry run listing the
selection). Several `Skip` options compose; the first one matching a vector
supplies its reason.

```go
for v := range runner.Run(ctx, selected,
    s3tests.Skip("ACLs not implemented by target", s3tests.Groups("acl")),
    s3tests.Skip("tier-3 multipart too slow here", s3tests.Groups("multipart"), s3tests.Tags("tier-3")),
    s3tests.Skip("tracked in issue #123", s3tests.IDs("object-crud-0017", "copy-0004")),
) {
    if v.Outcome == s3tests.Skipped {
        fmt.Printf("skipped %s: %s\n", v.ID, v.Reason)
    }
}
```

`SkipFunc` is the general form for when the reason varies per vector, e.g. a
skip-list mapping ids to the issue tracking each one:

```go
known := map[string]string{
    "multipart-0013":   "https://example.com/issues/123",
    "object-crud-0017": "https://example.com/issues/456",
}
skipKnown := s3tests.SkipFunc(func(v *s3vectors.Vector) (reason string, skip bool) {
    reason, skip = known[v.ID]
    return reason, skip
})
for v := range runner.Run(ctx, selected, skipKnown) { ... }
```

Test against real credentials for a *disposable* test account/tenant — the
runner creates and deletes buckets and objects, and a handful of object-lock
vectors create COMPLIANCE-retained objects whose buckets **cannot be deleted
until 2999** (they surface in `VectorResult.Warnings`; the `BucketPrefix`
makes them identifiable). Never point it at an account holding data you care
about.

## Prerequisites and identities

- `$bucket` / `$object` prerequisites are provisioned by `Config.Provisioner`
  — the built-in `DefaultProvisioner` uses the endpoint itself
  (`CreateBucket` / `PutObject`); supply your own implementation to provision
  out-of-band. Teardown (emptying and deleting each vector's buckets) is part
  of the same interface and always best-effort: problems become
  `VectorResult.Warnings`, never failures.
- `$credential` prerequisites need a second identity, which is
  server-specific: supply `Config.ProvisionCredential`. Without it, the ~56
  vectors requiring one report `blocked` (not `fail`), per the corpus's
  outcome semantics.
- Step identities `main`, `anonymous` (unsigned) and `invalid` (well-formed
  signature, unknown key) are handled internally.

## Outcomes and reporting

Outcome semantics follow the corpus spec: a failed *prerequisite* is
`blocked` and a violated *expectation* is `fail`. `skipped` marks a vector
deliberately not executed: `Run` produces it for vectors matched by a
`Skip`/`SkipFunc` option (with the option's reason), and consumers may also
synthesize it for vectors they filtered out before the run — either way
reports stay comparable across differently-selected runs. `VectorResult` carries the
stable vector id, group, tags and
per-step expected-vs-actual detail, and `Runner.CorpusVersion()` reports the
corpus snapshot — everything needed to emit JUnit XML/CTRF/TAP as
recommended in the corpus's
[reporting guide](https://github.com/cloud-portable/s3vectors/blob/main/docs/reporting.md).

`VectorResult.RunnerError` distinguishes "the runner could not execute this
vector" (unresolvable placeholder, unsupported operation) from a genuine
compatibility failure; such vectors still count as `fail`, but reports can
label them differently.

### Report formats

The [`report`](report) subpackage holds the shared run metadata
(`report.Meta`); each reporter lives in its own subpackage —
[`report/junit`](report/junit), [`report/html`](report/html) and
[`report/gotest`](report/gotest) today, CTRF and TAP planned. Formatters
consume the `iter.Seq` that `Run` returns, so a run can stream straight into
a report file:

```go
import (
    "github.com/cloud-portable/s3tests/packages/go/report"
    "github.com/cloud-portable/s3tests/packages/go/report/junit"
)

f, _ := os.Create("results.xml")
defer f.Close()
err := junit.Write(f, runner.Run(ctx, vectors), report.Meta{
    CorpusVersion: runner.CorpusVersion(),
    Target:        "MinIO RELEASE.2026-07-01",
})
```

(Or collect first and pass `slices.Values(results)` to format the same run
multiple ways.) For human inspection, `report/html` renders the same inputs
as a single self-contained HTML page — a dark-first dashboard (light theme
via `prefers-color-scheme`) with an overall outcome badge, a coverage-style
summary table of groups (pass-fraction bars, watermarked percentages), and
per-vector cards that open to expected-vs-actual detail and link to the
vector's definition in the corpus repo; groups with failures start expanded — with inline CSS, no JavaScript, and deterministic
output (no timestamps):

```go
import htmlreport "github.com/cloud-portable/s3tests/packages/go/report/html"

f, _ := os.Create("report.html")
defer f.Close()
err := htmlreport.Write(f, runner.Run(ctx, vectors), report.Meta{
    CorpusVersion: runner.CorpusVersion(),
    Target:        "MinIO RELEASE.2026-07-01",
    GeneratedAt:   time.Now(),
})
```

A runnable end-to-end example (endpoint/filter flags, used to produce the
committed sample reports for the full corpus and for `tier-1`) lives in
[`examples/htmlreport`](examples/htmlreport).

The JUnit mapping follows the reporting guide: one
`<testcase>` per vector (`classname` = group), `blocked` →
`<skipped message="blocked: ...">`, corpus version and target as suite
`<properties>`, vector tags as a per-case property. Vectors with a
`RunnerError` become JUnit `<error>` elements (test could not run), distinct
from `<failure>` (expectation violated). `Meta.OmitSkipped` drops
filter-skipped vectors from the report.

### Running under `go test`

The [`report/gotest`](report/gotest) subpackage reports each vector as a
`t.Run` subtest, so `go test` itself is the text reporter — plain output,
`-v` for per-vector detail, and `go test`-based CI comes for free:

```go
import "github.com/cloud-portable/s3tests/packages/go/report/gotest"

func TestS3Compat(t *testing.T) {
    runner, err := s3tests.New(cfg)
    if err != nil {
        t.Fatal(err)
    }
    vectors, err := s3tests.Vectors()
    if err != nil {
        t.Fatal(err)
    }
    gotest.Run(t, runner.Run(t.Context(), s3tests.ApplyFilters(vectors, s3tests.Groups("object-crud"))))
}
```

Passing vectors log their title and real duration (under `-v`), failures
`t.Fatal` with the failing step's expected-vs-actual detail, and
blocked/skipped vectors skip their subtest with a `blocked:`/`skipped:`
prefixed reason. Note `go test -run 'TestS3Compat/multipart-0007'` filters
which subtests are *reported*, not which vectors *execute* — select vectors
before the run instead (`s3tests.ApplyFilters`), or pass `s3tests.Skip`
options to `Run` to keep known-bad vectors visible as skipped subtests.

## Client behavior

The runner's SDK clients are tuned so the vectors own their wire bytes:
path-style addressing (default; `Config.VirtualHostStyle` switches), no
retries, no implicit request checksums or response checksum validation, no
`Expect: 100-continue`. `$http` steps are sent over raw sockets (the
wire-header vectors send headers `net/http` refuses to emit) and SigV4-signed
with the SDK's signer without path normalization.

## Known limitations

- `lifecycle-config-0010` uses `PutBucketLifecycle`, which aws-sdk-go-v2
  removed; it always reports `fail` with a `RunnerError`.
- `$matches` patterns are, per the spec, a portable subset valid in both
  ECMA-262 and RE2 (no lookarounds/backreferences), so they run on Go's
  stdlib `regexp`.
- Error codes that cannot appear on the wire (HEAD responses, 304s) are
  matched via a small status→code map.
- Multi-valued response headers are matched against their first value.
- `signing`-kind vectors (offline SigV4 algorithm tests) are out of scope for
  this runner and never loaded.

## Development

```
make test         # vet + unit tests + offline all-corpus smoke test
make integration  # runs curated groups against a disposable MinIO container
```

The offline corpus smoke test dry-runs all 1160 api vectors (interpolates
every placeholder, decodes every operation input, compiles every regex,
parses every capture path) and fails on any drift when the corpus version
bumps.
