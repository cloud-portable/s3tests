import test from 'node:test'
import assert from 'node:assert/strict'
import { Writable } from 'node:stream'
import { write } from '../report/junit.js'

const sampleResults = () => [
  {
    id: 'multipart-0001',
    group: 'multipart',
    title: 'two-part upload',
    tags: ['tier-1', 'multipart'],
    outcome: 'pass',
    duration: 1234e6
  },
  {
    id: 'multipart-0007',
    group: 'multipart',
    outcome: 'fail',
    steps: [
      { index: 0, name: 'CreateMultipartUpload', passed: true },
      {
        index: 2,
        name: 'CompleteMultipartUpload',
        failures: [
          { field: 'status', expected: '400', actual: '200' },
          { field: 'error', expected: 'InvalidPart & "<quoted>"', actual: '(no error)' }
        ]
      }
    ],
    duration: 0
  },
  {
    id: 'lifecycle-config-0010',
    group: 'lifecycle-config',
    outcome: 'fail',
    runnerError: 'operation PutBucketLifecycle is not supported',
    steps: [{ index: 1, name: 'PutBucketLifecycle', err: 'operation PutBucketLifecycle is not supported' }],
    duration: 0
  },
  { id: 'versioning-0003', group: 'versioning', outcome: 'blocked', reason: 'prerequisite $bucket b1: outage', duration: 0 },
  { id: 'versioning-0004', group: 'versioning', outcome: 'skipped', reason: 'excluded by tag filter', duration: 0 },
  { id: 'object-crud-0169', group: 'object-crud', outcome: 'pass', warnings: ['teardown x: BucketNotEmpty'], duration: 0 }
]

function render (results, meta) {
  const chunks = []
  const sink = new Writable({
    write (chunk, enc, cb) {
      chunks.push(Buffer.from(chunk))
      cb()
    }
  })
  return write(sink, results, meta).then(() => Buffer.concat(chunks).toString('utf8'))
}

test('mapping per the reporting guide', async () => {
  const out = await render(sampleResults(), {
    corpusVersion: '1.0.0',
    target: 'MinIO TEST',
    properties: { zeta: 'z', alpha: 'a' }
  })
  for (const want of [
    '<testsuites name="s3vectors" tests="6" failures="1" errors="1" skipped="2"',
    '<testsuite name="multipart" tests="2" failures="1" errors="0" skipped="0"',
    '<property name="corpusVersion" value="1.0.0"></property>',
    '<property name="target" value="MinIO TEST"></property>',
    '<testcase name="multipart-0001" classname="multipart" time="1.234">',
    '<property name="tags" value="tier-1,multipart"></property>',
    '<failure message="step 3 (CompleteMultipartUpload): status: expected 400, got 200">',
    'error: expected InvalidPart &amp; "&lt;quoted&gt;", got (no error)',
    '<error message="runner error: operation PutBucketLifecycle is not supported">',
    '<skipped message="blocked: prerequisite $bucket b1: outage"></skipped>',
    '<skipped message="skipped: excluded by tag filter"></skipped>',
    '<system-out>teardown x: BucketNotEmpty</system-out>'
  ]) {
    assert.ok(out.includes(want), `missing ${JSON.stringify(want)} in:\n${out}`)
  }
  // Properties sorted; blocked never a failure.
  assert.ok(out.indexOf('alpha') < out.indexOf('zeta'))
  assert.ok(!out.includes('<failure message="blocked'))
})

test('timestamp appears only when generatedAt set (UTC ISO 8601)', async () => {
  const when = new Date('2026-09-03T14:05:06+02:00')
  const out = await render(sampleResults(), { generatedAt: when })
  assert.ok(out.includes('timestamp="2026-09-03T12:05:06"'))
  const bare = await render(sampleResults(), {})
  assert.ok(!bare.includes('timestamp='))
})

test('omitSkipped', async () => {
  const out = await render(sampleResults(), { omitSkipped: true })
  assert.ok(!out.includes('versioning-0004'))
  assert.ok(out.includes('versioning-0003'), 'blocked kept')
  assert.ok(out.includes('tests="5"'))
})

test('deterministic and valid for an empty run', async () => {
  const meta = { corpusVersion: '1.0.0', properties: { b: '2', a: '1' } }
  assert.equal(await render(sampleResults(), meta), await render(sampleResults(), meta))
  const empty = await render([], {})
  assert.ok(empty.startsWith('<?xml version="1.0" encoding="UTF-8"?>'))
  assert.ok(empty.includes('tests="0"'))
})
