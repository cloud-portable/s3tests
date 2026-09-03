// Per-vector executor: establish prerequisites, run steps sequentially,
// always tear down. Corpus vectors are shared and cached — never mutated;
// interpolation rebuilds values.

import { randomBytes } from 'node:crypto'
import { Scope } from './interp.js'
import { DataCache } from './vdata.js'
import { contentValue } from './match.js'
import { runOperationStep } from './step-operation.js'
import { runHttpStep } from './step-http.js'

const nowNs = () => Number(process.hrtime.bigint())

/**
 * A result seeded with the vector's identifying metadata and no outcome
 * detail yet — the shape shared by executed and skipped vectors.
 * @param {object} vector corpus api vector
 * @param {string} [outcome]
 * @param {string} [reason]
 */
export function newResult (vector, outcome = 'pass', reason = '') {
  return {
    id: vector.id,
    group: vector.group,
    title: vector.title ?? '',
    tags: vector.tags ?? [],
    source: vector.source ?? '',
    outcome,
    reason,
    runnerError: '',
    steps: [],
    warnings: [],
    duration: 0
  }
}

/**
 * Execute one vector; never throws — problems become the result's outcome.
 * @param {object} rt runtime: {cfg, identities, target}
 * @param {object} vector corpus api vector
 * @param {AbortSignal} signal
 */
export async function runVector (rt, vector, signal) {
  const started = nowNs()
  const cache = new DataCache(vector.data)
  const run = {
    rt,
    vector,
    cache,
    scope: new Scope({
      env: { endpoint: rt.cfg.endpoint, region: rt.cfg.region },
      res: {},
      cap: {},
      data: (name, field) => cache.derived(name, field)
    }),
    buckets: [],
    signal,
    result: newResult(vector)
  }

  try {
    await execute(run)
  } finally {
    await teardown(run)
  }
  run.result.duration = nowNs() - started
  return run.result
}

async function execute (run) {
  for (const prereq of run.vector.prerequisites ?? []) {
    try {
      await establish(run, prereq)
    } catch (err) {
      run.result.outcome = 'blocked'
      run.result.reason = err.message
      return
    }
  }
  const steps = run.vector.steps ?? []
  for (let i = 0; i < steps.length; i++) {
    const sr = await runStep(run, i, steps[i])
    run.result.steps.push(sr)
    if (!sr.passed) {
      run.result.outcome = 'fail'
      return
    }
  }
}

// establish provisions one prerequisite and registers its resource
// attributes in the scope.
async function establish (run, prereq) {
  const { rt } = run
  if (prereq.$bucket) {
    const p = prereq.$bucket
    const name = bucketName(run, p.handle)
    let info
    try {
      info = await provisioner(rt).bucket(rt.target, p, name, { signal: run.signal })
    } catch (err) {
      throw new Error(`prerequisite $bucket ${p.handle}: ${err.message}`)
    }
    run.buckets.push({ name: info.name, knownKeys: info.knownKeys ?? [] })
    run.scope.res[p.handle] = { name: info.name }
    return
  }
  if (prereq.$object) {
    const p = prereq.$object
    const bucketAttrs = run.scope.res[p.bucket]
    if (!bucketAttrs) throw new Error(`prerequisite $object ${p.handle}: unknown bucket handle ${JSON.stringify(p.bucket)}`)
    let resolved, body
    try {
      resolved = {
        ...p,
        key: run.scope.string(p.key),
        contentType: p.contentType ? run.scope.string(p.contentType) : undefined,
        metadata: p.metadata
          ? Object.fromEntries(Object.entries(p.metadata).map(([k, v]) => [k, run.scope.string(v)]))
          : undefined
      }
      body = p.body !== undefined
        ? contentValue(run.scope.value(p.body), (n) => run.cache.bytes(n))
        : null
    } catch (err) {
      throw new Error(`prerequisite $object ${p.handle}: ${err.message}`)
    }
    let info
    try {
      info = await provisioner(rt).object(rt.target, resolved, bucketAttrs.name, body, { signal: run.signal })
    } catch (err) {
      throw new Error(`prerequisite $object ${p.handle}: ${err.message}`)
    }
    run.scope.res[p.handle] = { key: info.key, etag: info.etag, versionId: info.versionId }
    trackKey(run, bucketAttrs.name, info.key)
    return
  }
  if (prereq.$credential) {
    const handle = prereq.$credential.handle
    let cred
    try {
      cred = await rt.identities.provisionAlt(handle)
    } catch (err) {
      throw new Error(`prerequisite $credential ${handle}: ${err.message}`)
    }
    run.scope.res[handle] = {
      accessKeyId: cred.accessKeyId ?? '',
      canonicalId: cred.canonicalId ?? '',
      displayName: cred.displayName ?? ''
    }
    return
  }
  throw new Error('prerequisite with no $bucket/$object/$credential key')
}

async function runStep (run, index, step) {
  const started = nowNs()
  const sr = {
    index,
    kind: '',
    name: '',
    presigned: false,
    identity: '',
    status: 0,
    passed: false,
    failures: [],
    err: '',
    captured: null,
    duration: 0
  }
  if (step.$operation) {
    await runOperationStep(run, step.$operation, sr)
  } else if (step.$http) {
    await runHttpStep(run, step.$http, sr)
  } else {
    sr.err = 'step with no $operation/$http key'
    run.result.runnerError = sr.err
  }
  sr.duration = nowNs() - started
  sr.passed = sr.err === '' && sr.failures.length === 0
  return sr
}

function provisioner (rt) {
  return rt.cfg.provisioner ?? rt.defaultProvisioner
}

/** Record a runner/vector-definition error (not a compat failure). */
export function runnerFail (run, sr, err) {
  sr.err = err.message
  run.result.runnerError = err.message
}

/**
 * Register a bucket created by a *step* (CreateBucket, or a raw PUT on a
 * bucket path) so teardown covers it like prerequisite buckets.
 */
export function trackBucket (run, name) {
  if (!name || run.buckets.some((b) => b.name === name)) return
  run.buckets.push({ name, knownKeys: [] })
}

/**
 * Record an object key the runner wrote, giving teardown a way to delete
 * keys that server listings fail to surface.
 */
export function trackKey (run, bucket, key) {
  if (!bucket || !key) return
  const b = run.buckets.find((b) => b.name === bucket)
  if (b && !b.knownKeys.includes(key)) b.knownKeys.push(key)
}

// bucketName picks a unique, valid bucket name: prefix + vector id + handle +
// random suffix, lowercased and trimmed to the 63-char limit.
function bucketName (run, handle) {
  let name = (run.rt.cfg.bucketPrefix + run.vector.id + '-' + handle)
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, '-')
  const tail = '-' + randomBytes(4).toString('hex')
  if (name.length + tail.length > 63) name = name.slice(0, 63 - tail.length)
  return name + tail
}

async function teardown (run) {
  if (run.rt.cfg.keepResources || run.buckets.length === 0) return
  // Cancellation must not leak buckets: teardown runs on its own timeout.
  try {
    const warnings = await provisioner(run.rt).teardown(
      run.rt.target, run.buckets, { signal: AbortSignal.timeout(120_000) })
    run.result.warnings.push(...warnings)
  } catch (err) {
    run.result.warnings.push('teardown: ' + err.message)
  }
}
