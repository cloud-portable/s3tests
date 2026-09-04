# s3tests

A runner for the language-independent
[S3 compatibility test vectors](https://github.com/cloud-portable/s3vectors).
It executes every `api`-kind vector against an S3 endpoint — provisioning
prerequisites, interpolating placeholders, dispatching operations through
`@aws-sdk/client-s3`, sending raw wire-level requests for `$http` steps,
minting presigned URLs — and evaluates the corpus's expectation matchers,
reporting one of four outcomes per vector: `pass`, `fail`, `blocked` or
`skipped`.

Requires Node.js 22+. Plain ESM, no build step; types ship as hand-written
`.d.ts` files.

## Usage

### CLI

```sh
npm install -g @cloud-portable/s3tests

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
provenance.

### Programmatic

```sh
npm install @cloud-portable/s3tests
```

```js
import { Runner, vectors, applyFilters, groups, tags, excludeIds } from '@cloud-portable/s3tests'

const runner = new Runner({
  endpoint: 'http://127.0.0.1:9000',
  credentials: { accessKeyId: 'YOUR_ACCESS_KEY_ID', secretAccessKey: 'YOUR_SECRET_ACCESS_KEY' }
})

// Select what to run: filters are plain predicates ANDed together by
// applyFilters, so custom selections compose with the built-ins.
const selected = applyFilters(vectors(), // the whole corpus, in manifest order
  groups('object-crud', 'multipart'),
  tags('tier-1'),                        // vector has ≥1 listed tag
  excludeIds('multipart-0013'),          // dropped: leaves no trace in results
  (v) => v.steps.length < 20             // custom
)

// run() executes exactly the vectors given, streaming one VectorResult per
// vector as it completes. Breaking out of the loop, or aborting the signal,
// cancels the rest of the run; in-flight vectors still tear down.
// (run() also takes skip rules — see "Skipping vectors" below.)
const counts = { pass: 0, fail: 0, blocked: 0, skipped: 0 }
const failures = []
for await (const v of runner.run(selected)) {
  console.log(v.outcome.padEnd(8), v.id)
  counts[v.outcome]++
  if (v.outcome === 'fail') failures.push(v)
}

console.log(`corpus ${runner.corpusVersion()}: ${counts.pass} pass, ` +
  `${counts.fail} fail, ${counts.blocked} blocked, ${counts.skipped} skipped`)
for (const v of failures) {
  const step = v.steps.at(-1)
  console.log(`${v.id} step ${v.steps.length} (${step.name}):`)
  for (const f of step.failures) {
    console.log(`  ${f.field}: expected ${f.expected}, got ${f.actual}`)
  }
}
```

#### Skipping vectors

Filtering with `applyFilters` *drops* vectors: they never reach `run()` and
leave no trace in the results. When you want a vector recorded but not
executed — a known server bug, a feature the target doesn't implement —
pass skip rules in `run()`'s `skip` option instead. Skipped vectors are never
sent to the server but still yield a `VectorResult` in their normal position,
carrying the vector's id, group, title and tags, with `outcome === 'skipped'`,
your reason in `reason`, no steps and zero duration. Every reporter renders
them, so runs stay comparable and the skip-list is visible in the report.

`skip(reason, ...filters)` builds a rule from a reason plus the same filter
predicates as `applyFilters` (ANDed, so `skip(reason)` with no filters skips
everything — a dry run listing the selection). Rules are consulted in order;
the first one matching a vector supplies its reason.

```js
import { skip, groups, tags, ids } from '@cloud-portable/s3tests'

for await (const v of runner.run(selected, {
  skip: [
    skip('ACLs not implemented by target', groups('acl')),
    skip('tier-3 multipart too slow here', groups('multipart'), tags('tier-3')),
    skip('tracked in issue #123', ids('object-crud-0017', 'copy-0004'))
  ]
})) {
  if (v.outcome === 'skipped') console.log(`skipped ${v.id}: ${v.reason}`)
}
```

A rule is just a function `(vector) => string | undefined` — a string is the
reason to skip, `undefined` lets the vector run — so for reasons that vary per
vector write one by hand, e.g. a skip-list mapping ids to the issue tracking
each one:

```js
const known = {
  'multipart-0013': 'https://example.com/issues/123',
  'object-crud-0017': 'https://example.com/issues/456'
}
for await (const v of runner.run(selected, { skip: [(v) => known[v.id]] })) { ... }
```

Test against real credentials for a *disposable* test account/tenant — the
runner creates and deletes buckets and objects, and a handful of object-lock
vectors create COMPLIANCE-retained objects whose buckets **cannot be deleted
until 2999** (they surface in `VectorResult.warnings`; the `bucketPrefix`
makes them identifiable). Never point it at an account holding data you care
about.

## Prerequisites and identities

- `$bucket` / `$object` prerequisites are provisioned by `config.provisioner`
  — the built-in `defaultProvisioner` uses the endpoint itself
  (`CreateBucket` / `PutObject`); supply your own implementation to provision
  out-of-band. Teardown (emptying and deleting each vector's buckets) is part
  of the same interface and always best-effort: problems become
  `VectorResult.warnings`, never failures.
- `$credential` prerequisites need a second identity, which is
  server-specific: supply `config.provisionCredential`. Without it, the ~56
  vectors requiring one report `blocked` (not `fail`), per the corpus's
  outcome semantics.
- Step identities `main`, `anonymous` (unsigned) and `invalid` (well-formed
  signature, unknown key) are handled internally.

## Outcomes and reporting

Outcome semantics follow the corpus spec: a failed *prerequisite* is
`blocked` and a violated *expectation* is `fail`. `skipped` marks a vector
deliberately not executed: `run()` produces it for vectors matched by a skip
rule (with the rule's reason), and consumers may also synthesize it for
vectors they filtered out before the run — either way reports stay comparable
across differently-selected runs. `VectorResult` carries the
stable vector id, group, tags and per-step expected-vs-actual detail, and
`runner.corpusVersion()` reports the corpus snapshot — everything needed to
emit JUnit XML/CTRF/TAP as recommended in the corpus's
[reporting guide](https://github.com/cloud-portable/s3vectors/blob/main/docs/reporting.md).

`VectorResult.runnerError` distinguishes "the runner could not execute this
vector" (unresolvable placeholder, unsupported operation) from a genuine
compatibility failure; such vectors still count as `fail`, but reports can
label them differently.

### Report formats

Each reporter is its own export — `report/junit`, `report/html` and
`report/nodetest` today, CTRF and TAP planned. The `write` functions consume
the async iterator that `run()` returns, so a run can stream straight into a
report file:

```js
import { createWriteStream } from 'node:fs'
import * as junit from '@cloud-portable/s3tests/report/junit'

const f = createWriteStream('results.xml')
await junit.write(f, runner.run(selected), {
  corpusVersion: runner.corpusVersion(),
  target: 'MinIO RELEASE.2026-07-01'
})
f.end()
```

(Or collect into an array first to format the same run multiple ways.) For
human inspection, `report/html` renders the same inputs as a single
self-contained HTML page — a dark-first dashboard (light theme via
`prefers-color-scheme`) with an overall outcome badge, a coverage-style
summary table of groups (pass-fraction bars, watermarked percentages), and
per-vector cards that open to expected-vs-actual detail and link to the
vector's definition in the corpus repo; groups with failures start expanded — with inline CSS, no JavaScript, and deterministic
output. The page template is shared with the Go runner
([`shared/report/page.mustache`](../../shared/report/page.mustache)) and the
two implementations produce **byte-identical** HTML for the same results —
a golden test in each package pins that.

```js
import * as html from '@cloud-portable/s3tests/report/html'

const f = createWriteStream('report.html')
await html.write(f, runner.run(selected), {
  corpusVersion: runner.corpusVersion(),
  target: 'MinIO RELEASE.2026-07-01',
  generatedAt: new Date()
})
f.end()
```

A runnable end-to-end example (endpoint/filter flags, used to produce the
committed sample reports for the full corpus and for `tier-1`) lives in
[`examples/htmlreport`](examples/htmlreport).

The JUnit mapping follows the reporting guide: one `<testcase>` per vector
(`classname` = group), `blocked` → `<skipped message="blocked: ...">`, corpus
version and target as suite `<properties>`, vector tags as a per-case
property. Vectors with a `runnerError` become JUnit `<error>` elements (test
could not run), distinct from `<failure>` (expectation violated).
`meta.omitSkipped` drops filter-skipped vectors from the report.

### Running under `node --test`

The `report/nodetest` export reports each vector as a `node:test` subtest,
so the built-in test runner is the text reporter — spec/TAP output, reporter
plugins, and `node --test`-based CI come for free:

```js
import test from 'node:test'
import { run } from '@cloud-portable/s3tests/report/nodetest'

test('s3 compatibility', async (t) => {
  const runner = new Runner(config)
  await run(t, runner.run(applyFilters(vectors(), groups('object-crud'))))
})
```

Passing vectors log their title and real duration as a diagnostic, failures
throw with the failing step's expected-vs-actual detail, and blocked/skipped
vectors skip their subtest with a `blocked:`/`skipped:` prefixed reason. Note
`node --test --test-name-pattern` filters which subtests are *reported*, not
which vectors *execute* — select vectors before the run instead
(`applyFilters`), or pass skip rules to `run()` to keep known-bad vectors
visible as skipped subtests.

## Client behavior

The runner's SDK clients are tuned so the vectors own their wire bytes:
path-style addressing (default; `config.virtualHostStyle` switches), no
retries (`maxAttempts: 1`), no implicit request checksums or response
checksum validation, no `Expect: 100-continue`. `$http` steps are sent over
raw sockets (the wire-header vectors send headers Node's HTTP client refuses
to emit) and SigV4-signed with `@smithy/signature-v4` without path
normalization.

## Known limitations

- `lifecycle-config-0010` uses `PutBucketLifecycle`, which
  `@aws-sdk/client-s3` (v3) removed; it always reports `fail` with a
  `runnerError`.
- aws-sdk-js-v3 carries no runtime operation schemas, so input coercion
  (timestamps, streamed bodies, stringified numbers) is driven by key sets
  derived from the corpus; the offline all-corpus smoke test pins that every
  vector's inputs decode, and fails on any drift when the corpus version
  bumps.
- Presigned GET/HEAD URLs are fetched with `fetch()`; the SDK hoists some
  headers into query parameters, matching the Go runner's behavior.
- `$matches` patterns are, per the spec, a portable subset valid in both
  ECMA-262 and RE2 (no lookarounds/backreferences), so they run on the
  built-in `RegExp`.
- Error codes that cannot appear on the wire (HEAD responses, 304s) are
  matched via a small status→code map.
- Multi-valued response headers are matched against their first value.
- `signing`-kind vectors (offline SigV4 algorithm tests) are out of scope for
  this runner and never loaded.

## Development

```
make test         # unit tests + offline all-corpus smoke test
make integration  # runs curated groups against a disposable MinIO container
```

`npm test` also verifies the shared HTML template is in sync
(`scripts/sync-report-template.js --check`) and byte-compares the HTML
reporter's output against the cross-language golden file in
[`shared/report`](../../shared/report).
