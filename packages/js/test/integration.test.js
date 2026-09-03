// Integration test against a real S3 implementation (MinIO by default; see
// the Makefile's `integration` target). It asserts runner *mechanics* — no
// unexpected runner errors, sane outcome accounting, clean teardown — not the
// target's compatibility score. Self-skips unless S3TESTS_ENDPOINT is set.

import test from 'node:test'
import assert from 'node:assert/strict'
import { Writable } from 'node:stream'
import { S3Client, ListBucketsCommand } from '@aws-sdk/client-s3'
import { Runner, vectors, applyFilters, groups } from '../index.js'
import * as junit from '../report/junit.js'
import * as html from '../report/html.js'

const endpoint = process.env.S3TESTS_ENDPOINT

// Vectors whose failure is a known runner limitation, not a target problem.
const ALLOWED_RUNNER_ERRORS = new Set(['lifecycle-config-0010'])

test('integration', { skip: !endpoint && 'S3TESTS_ENDPOINT not set' }, async (t) => {
  const credentials = {
    accessKeyId: process.env.S3TESTS_ACCESS_KEY ?? 'minioadmin',
    secretAccessKey: process.env.S3TESTS_SECRET_KEY ?? 'minioadmin'
  }
  const runner = new Runner({ endpoint, credentials, concurrency: 4 })

  const groupNames = (process.env.S3TESTS_GROUPS ??
    'object-crud,multipart,presigned,anon-access,checksums,wire-headers,cors').split(',')
  const selected = applyFilters(vectors(), groups(...groupNames))
  assert.ok(selected.length > 0, 'group filter selected nothing')

  const counts = { pass: 0, fail: 0, blocked: 0, skipped: 0 }
  const collected = []
  for await (const res of runner.run(selected)) {
    counts[res.outcome]++
    collected.push(res)
    if (res.runnerError && !ALLOWED_RUNNER_ERRORS.has(res.id)) {
      assert.fail(`${res.id}: unexpected runner error: ${res.runnerError}`)
    }
    for (const w of res.warnings) t.diagnostic(`${res.id}: warning: ${w}`)
    // No credential provisioner is configured, so $credential vectors must
    // be blocked, and nothing else should be.
    if (res.outcome === 'blocked' && !/\$credential|provisionCredential/.test(res.reason)) {
      assert.fail(`${res.id}: unexpected block: ${res.reason}`)
    }
  }
  const total = collected.length
  assert.equal(total, selected.length, 'run must yield one result per selected vector')
  assert.equal(counts.pass + counts.fail + counts.blocked + counts.skipped, total)
  assert.ok(counts.pass > 0, 'no vector passed — endpoint misconfigured?')
  t.diagnostic(`corpus ${runner.corpusVersion()} against ${endpoint}: ` +
    `pass=${counts.pass} fail=${counts.fail} blocked=${counts.blocked}`)

  // Both reporters must render the real results.
  const meta = { corpusVersion: runner.corpusVersion(), target: endpoint }
  const renderWith = async (writer) => {
    const chunks = []
    const sink = new Writable({
      write (chunk, enc, cb) {
        chunks.push(Buffer.from(chunk))
        cb()
      }
    })
    await writer(sink, collected, meta)
    return Buffer.concat(chunks).toString('utf8')
  }
  const junitOut = await renderWith(junit.write)
  assert.ok(junitOut.includes(`tests="${total}"`))
  const htmlOut = await renderWith(html.write)
  assert.ok(htmlOut.length > 10_000 && htmlOut.includes('id="group-multipart"'))
  assert.ok(!htmlOut.includes('<script'), 'html report must contain no JavaScript')

  // Teardown audit: no runner buckets may survive (the curated groups
  // contain no COMPLIANCE-retention vectors). "s3tests-" is the documented
  // default bucketPrefix.
  const audit = new S3Client({ endpoint, region: 'us-east-1', forcePathStyle: true, credentials })
  const { Buckets = [] } = await audit.send(new ListBucketsCommand({}))
  const leaked = Buckets.filter((b) => b.Name.startsWith('s3tests-'))
  assert.deepEqual(leaked.map((b) => b.Name), [], 'teardown leaked buckets')
})
