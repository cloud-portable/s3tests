// node:test adapter: report each vector result as a subtest, so `node --test`
// itself renders the outcome (parity with the Go runner's report/gotest).
//
//   import { test } from 'node:test'
//   import { run } from '@cloud-portable/s3tests/report/nodetest'
//
//   test('s3 compatibility', async (t) => {
//     const runner = new Runner(config)
//     await run(t, runner.run(applyFilters(vectors(), groups('object-crud'))))
//   })
//
// Note that node:test name filters select which subtests are *reported*, not
// which vectors *execute* — the runner has already produced every result by
// the time its subtest runs. Select vectors before the run (applyFilters).

/**
 * Report each vector result as a subtest of t, named by the vector id. A
 * pass returns (logging title and the vector's real duration), a fail throws
 * with the failing step's expected/actual detail, and blocked or skipped
 * vectors skip the subtest with a prefixed reason.
 * @param {import('node:test').TestContext} t
 * @param {Iterable<object> | AsyncIterable<object>} results
 */
export async function run (t, results) {
  for await (const res of toAsync(results)) {
    await t.test(res.id, (t2) => report(t2, res))
  }
}

// report maps one result onto the subtest context. Exported for tests.
export function report (t, res) {
  for (const w of res.warnings ?? []) t.diagnostic(`warning: ${w}`)
  switch (res.outcome) {
    case 'pass':
      // The subtest's own wall time is ~0 (execution already happened in the
      // runner), so log the vector's real duration.
      t.diagnostic(`${res.title} (${(res.duration / 1e9).toFixed(3)}s)`)
      return
    case 'fail':
      if (res.runnerError) throw new Error(`runner error: ${res.runnerError}`)
      throw new Error(failureDetail(res))
    case 'blocked':
      t.skip(`blocked: ${res.reason}`)
      return
    case 'skipped':
      t.skip(`skipped: ${res.reason}`)
      return
    default:
      throw new Error(`unknown outcome ${JSON.stringify(res.outcome)}`)
  }
}

// failureDetail renders the failing (last executed) step: a "step N (name):"
// header followed by one line per expectation mismatch (identical text to
// the Go runner's gotest adapter).
function failureDetail (res) {
  if (!res.steps?.length) return res.reason ?? 'failed'
  const step = res.steps[res.steps.length - 1]
  let out = `step ${step.index + 1} (${step.name}):`
  if (step.err) out += `\n  ${step.err}`
  for (const f of step.failures ?? []) out += `\n  ${f.field}: expected ${f.expected}, got ${f.actual}`
  return out
}

async function * toAsync (iterable) {
  yield * iterable
}
