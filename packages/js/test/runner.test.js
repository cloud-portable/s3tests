import test from 'node:test'
import assert from 'node:assert/strict'
import { Runner, vectors, applyFilters, groups, tags, ids, excludeGroups, excludeTags, excludeIds, skip } from '../index.js'
import { startFakeS3 } from './helpers/fake-s3.js'

const passVector = () => ({
  id: 'test-0001',
  group: 'test',
  kind: 'api',
  title: 'get seeded object',
  tags: ['tier-1', 'test'],
  prerequisites: [
    { $bucket: { handle: 'b1' } },
    { $object: { handle: 'o1', bucket: 'b1', key: 'hello.txt', body: 'hello world' } }
  ],
  data: { pat: { $pattern: { pattern: 'hello world', size: 11 } } },
  steps: [
    {
      $operation: {
        name: 'GetObject',
        params: { Bucket: '${res.b1.name}', Key: '${res.o1.key}' },
        capture: { etag: 'ETag' },
        expect: {
          status: 200,
          response: { ContentLength: 11, ETag: '${data.pat.etag}' },
          headers: { etag: { $exists: true } },
          body: { $data: 'pat' }
        }
      }
    },
    {
      $operation: {
        name: 'HeadObject',
        params: { Bucket: '${res.b1.name}', Key: '${res.o1.key}' },
        expect: { response: { ETag: '${cap.etag}' } }
      }
    },
    {
      $operation: {
        name: 'GetObject',
        params: { Bucket: '${res.b1.name}', Key: 'missing.bin' },
        expect: { status: 404, error: 'NoSuchKey' }
      }
    }
  ]
})

const newRunner = (url, extra = {}) => new Runner({
  endpoint: url,
  credentials: { accessKeyId: 'AK', secretAccessKey: 'SK' },
  ...extra
})

async function runOne (runner, vector) {
  for await (const res of runner.run([vector])) return res
  throw new Error('no result yielded')
}

test('pass vector: prereqs, captures, expectations, teardown', async () => {
  const srv = await startFakeS3()
  try {
    const res = await runOne(newRunner(srv.url), passVector())
    assert.equal(res.outcome, 'pass', JSON.stringify(res, null, 1))
    assert.equal(res.steps.length, 3)
    assert.ok(res.steps[0].captured.etag)
    assert.deepEqual(res.warnings, [])
    assert.equal(srv.buckets.size, 0, 'teardown must remove the bucket')
  } finally {
    await srv.close()
  }
})

test('fail vector aborts at the failing step without RunnerError', async () => {
  const srv = await startFakeS3()
  try {
    const v = passVector()
    v.steps[0].$operation.expect.response = { ContentLength: 999 }
    const res = await runOne(newRunner(srv.url), v)
    assert.equal(res.outcome, 'fail')
    assert.equal(res.runnerError, '')
    assert.equal(res.steps.length, 1, 'failing step must abort the vector')
    assert.equal(res.steps[0].failures.length, 1)
    assert.equal(res.steps[0].failures[0].field, 'response.ContentLength')
  } finally {
    await srv.close()
  }
})

test('prerequisite failure blocks; steps never run', async () => {
  const srv = await startFakeS3()
  try {
    const runner = newRunner(srv.url, {
      provisioner: {
        bucket: async () => { throw new Error('simulated provisioning outage') },
        object: async () => { throw new Error('unreachable') },
        teardown: async () => []
      }
    })
    const res = await runOne(runner, passVector())
    assert.equal(res.outcome, 'blocked')
    assert.match(res.reason, /prerequisite \$bucket b1/)
    assert.equal(res.steps.length, 0)
  } finally {
    await srv.close()
  }
})

test('$credential without provisionCredential blocks', async () => {
  const srv = await startFakeS3()
  try {
    const res = await runOne(newRunner(srv.url), {
      id: 'test-0002',
      group: 'test',
      kind: 'api',
      tags: ['tier-1'],
      prerequisites: [{ $credential: { handle: 'alt' } }],
      steps: [{ $operation: { name: 'ListBuckets', identity: 'alt' } }]
    })
    assert.equal(res.outcome, 'blocked')
  } finally {
    await srv.close()
  }
})

test('unresolvable placeholder is a runner error', async () => {
  const srv = await startFakeS3()
  try {
    const v = passVector()
    v.steps[0].$operation.params.Key = '${cap.neverCaptured}'
    const res = await runOne(newRunner(srv.url), v)
    assert.equal(res.outcome, 'fail')
    assert.match(res.runnerError, /unresolvable placeholder/)
  } finally {
    await srv.close()
  }
})

test('vectors() loads the api corpus with groups', () => {
  const all = vectors()
  assert.ok(all.length > 1000, `only ${all.length} vectors`)
  for (const v of all) {
    assert.notEqual(v.group, 'signing')
    assert.equal(v.kind, 'api')
    assert.ok(v.group && v.id)
  }
})

test('applyFilters composes with AND semantics', () => {
  const all = vectors()
  assert.equal(applyFilters(all).length, all.length)
  const one = applyFilters(all, ids('object-crud-0001'))
  assert.equal(one.length, 1)
  assert.equal(one[0].id, 'object-crud-0001')
  const mp = applyFilters(all, groups('multipart'))
  assert.ok(mp.length > 0 && mp.every((v) => v.group === 'multipart'))
  const tier1mp = applyFilters(all, groups('multipart'), tags('tier-1'))
  assert.ok(tier1mp.length > 0 && tier1mp.length < mp.length)
  assert.equal(applyFilters(mp, excludeIds(mp[0].id)).length, mp.length - 1)
  assert.equal(applyFilters(mp, excludeGroups('multipart')).length, 0)
  assert.equal(applyFilters(tier1mp, excludeTags('tier-1')).length, 0)
  const custom = applyFilters(all, (v) => (v.steps ?? []).length > 10)
  assert.ok(custom.every((v) => v.steps.length > 10))
})

test('run yields exactly the given vectors; breaking cancels', async () => {
  const srv = await startFakeS3()
  try {
    const runner = newRunner(srv.url, { concurrency: 2 })
    const list = [passVector(), { ...passVector(), id: 'test-0003' }, { ...passVector(), id: 'test-0004' }]
    let seen = 0
    for await (const res of runner.run(list)) {
      assert.notEqual(res.outcome, 'skipped', 'run must not skip vectors without a skip rule')
      seen++
      break // must cancel outstanding work and still tear down
    }
    assert.equal(seen, 1)
    // Teardowns completed before the generator returned.
    assert.equal(srv.buckets.size, 0, 'break must not leak buckets')
  } finally {
    await srv.close()
  }
})

// Skip rules record matching vectors as skipped — with the vector's metadata
// and the rule's reason, no steps, and nothing sent to the server — while
// everything else runs as normal.
test('skip rules record vectors as skipped without running them', async () => {
  const srv = await startFakeS3()
  try {
    const runner = newRunner(srv.url)
    const list = [
      passVector(),
      { ...passVector(), id: 'test-0002', title: 'second' },
      { ...passVector(), id: 'test-0003' }
    ]
    const perVector = { 'test-0002': 'tracked in issue #42' }

    const results = []
    for await (const res of runner.run(list, {
      skip: [
        skip('known bug', ids('test-0001')),
        (v) => perVector[v.id],
        skip('shadowed', ids('test-0001')) // a later rule never overrides an earlier match
      ]
    })) results.push(res)

    // Concurrency 1: skipped vectors hold their place in the stream.
    assert.deepEqual(results.map((r) => r.id), ['test-0001', 'test-0002', 'test-0003'])

    const [one, two, three] = results
    assert.equal(one.outcome, 'skipped')
    assert.equal(one.reason, 'known bug')
    assert.equal(two.outcome, 'skipped')
    assert.equal(two.reason, 'tracked in issue #42')
    assert.equal(three.outcome, 'pass')
    assert.equal(three.reason, '')
    for (const res of [one, two]) {
      const v = list.find((x) => x.id === res.id)
      assert.equal(res.group, v.group)
      assert.equal(res.title, v.title)
      assert.deepEqual(res.tags, v.tags)
      assert.equal(res.steps.length, 0, 'skipped vector must not execute steps')
      assert.equal(res.duration, 0)
      assert.equal(res.runnerError, '')
    }
    // Only the executed vector touched the server.
    assert.equal(srv.buckets.size, 0)

    // skip() with no filters is a dry run: everything is skipped.
    const dry = []
    for await (const res of runner.run(list, { skip: [skip('dry run')] })) dry.push(res)
    assert.deepEqual(dry.map((r) => [r.outcome, r.reason]), list.map(() => ['skipped', 'dry run']))
  } finally {
    await srv.close()
  }
})

test('pre-aborted signal yields nothing', async () => {
  const srv = await startFakeS3()
  try {
    const ac = new AbortController()
    ac.abort()
    let count = 0
    for await (const _ of newRunner(srv.url).run([passVector()], { signal: ac.signal })) count++ // eslint-disable-line no-unused-vars
    assert.equal(count, 0)
  } finally {
    await srv.close()
  }
})
