import test from 'node:test'
import assert from 'node:assert/strict'
import { run, report } from '../report/nodetest.js'

/** Recorder implementing the subset of TestContext that report() drives. */
const recorder = () => {
  const r = { logs: [], skipped: '', events: [] }
  r.diagnostic = (msg) => {
    r.logs.push(msg)
    r.events.push('log')
  }
  r.skip = (msg) => {
    r.skipped = msg
    r.events.push('skip')
  }
  return r
}

test('pass logs title and real duration', () => {
  const r = recorder()
  report(r, { id: 'x', outcome: 'pass', title: 'put then get', duration: 1234e6 })
  assert.deepEqual(r.logs, ['put then get (1.234s)'])
  assert.equal(r.skipped, '')
})

test('fail throws with expected/actual detail', () => {
  const r = recorder()
  assert.throws(() => report(r, {
    id: 'x',
    outcome: 'fail',
    steps: [{
      index: 2,
      name: 'CompleteMultipartUpload',
      err: 'transport hiccup',
      failures: [
        { field: 'status', expected: '400', actual: '200' },
        { field: 'error', expected: 'InvalidPart', actual: '(no error)' }
      ]
    }],
    duration: 0
  }), (err) => err.message === 'step 3 (CompleteMultipartUpload):\n' +
    '  transport hiccup\n' +
    '  status: expected 400, got 200\n' +
    '  error: expected InvalidPart, got (no error)')
})

test('runner error throws with prefix', () => {
  assert.throws(
    () => report(recorder(), { id: 'x', outcome: 'fail', runnerError: 'operation X is not supported', duration: 0 }),
    /^Error: runner error: operation X is not supported$/)
})

test('blocked and skipped skip with prefixed reasons; warnings log first', () => {
  let r = recorder()
  report(r, { id: 'x', outcome: 'blocked', reason: 'prerequisite $bucket b1: down', duration: 0 })
  assert.equal(r.skipped, 'blocked: prerequisite $bucket b1: down')

  r = recorder()
  report(r, {
    id: 'x',
    outcome: 'skipped',
    reason: 'excluded',
    warnings: ['teardown x: leftover'],
    duration: 0
  })
  assert.equal(r.skipped, 'skipped: excluded')
  assert.deepEqual(r.events, ['log', 'skip'], 'warnings must log before the terminal call')
})

// Real node:test integration: pass/blocked/skipped results execute as named
// subtests without failing the suite (the throw path is recorder-only — a
// real one would fail this run).
test('run drives real subtests', async (t) => {
  await run(t, [
    { id: 'object-crud-0001', outcome: 'pass', title: 'put then get', duration: 42e6 },
    { id: 'versioning-0003', outcome: 'blocked', reason: 'no alt credential', duration: 0 },
    { id: 'versioning-0004', outcome: 'skipped', reason: 'excluded', duration: 0 }
  ])
})
