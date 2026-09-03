// The offline corpus smoke test dry-runs every api vector — no network —
// proving the runner can process it: every placeholder resolves, every
// operation is a known command with coercible params, every $matches regex
// compiles, every capture path parses, every content descriptor and dataset
// materializes. It is the drift alarm for corpus version bumps.

import test from 'node:test'
import assert from 'node:assert/strict'
import { vectors } from '../index.js'
import { Scope } from '../lib/interp.js'
import { DataCache } from '../lib/vdata.js'
import { contentValue, compileRegex } from '../lib/match.js'
import { buildInput } from '../lib/coerce.js'
import { supported } from '../lib/dispatch.js'
import { presignSupported } from '../lib/presign.js'
import { parse as parsePath } from '../lib/jsonpath.js'

// The corpus's single known runner limitation, identical to the Go runner.
const ALLOWED = new Set([
  'lifecycle-config-0010: step 2: operation PutBucketLifecycle is not supported by @aws-sdk/client-s3'
])

test('corpus smoke: every api vector is executable', () => {
  const all = vectors()
  assert.ok(all.length > 1000, `only ${all.length} api vectors — corpus load broken?`)
  const problems = []
  for (const v of all) {
    for (const p of smokeVector(v)) problems.push(`${v.id}: ${p}`)
  }
  const unexpected = problems.filter((p) => !ALLOWED.has(p))
  assert.deepEqual(unexpected, [])
  assert.equal(problems.length, ALLOWED.size,
    `expected exactly the known problem(s), found: ${JSON.stringify(problems)}`)
})

function smokeVector (v) {
  const problems = []
  const fail = (msg) => problems.push(msg)
  const cache = new DataCache(v.data)
  const scope = new Scope({
    env: { endpoint: 'http://smoke.invalid:9000', region: 'us-east-1' },
    res: {},
    cap: {},
    data: (name, field) => cache.derived(name, field)
  })

  // Register prerequisite resource attributes exactly as the runner would.
  for (const [i, prereq] of (v.prerequisites ?? []).entries()) {
    if (prereq.$bucket) {
      scope.res[prereq.$bucket.handle] = { name: 'smoke-bucket' }
    } else if (prereq.$object) {
      const p = prereq.$object
      scope.res[p.handle] = { key: p.key, etag: '"d41d8cd98f00b204e9800998ecf8427e"', versionId: 'smoke-version' }
      if (p.body !== undefined) {
        try {
          contentValue(scope.value(p.body), (n) => cache.bytes(n))
        } catch (err) {
          fail(`object prerequisite ${p.handle} body: ${err.message}`)
        }
      }
    } else if (prereq.$credential) {
      scope.res[prereq.$credential.handle] = { accessKeyId: 'SMOKEKEY', canonicalId: 'smoke-canonical', displayName: 'smoke' }
    } else {
      fail(`prerequisite ${i} has no union key`)
    }
  }

  // Pre-seed every ${cap.*} reference; the value must survive every context
  // captures are re-injected into, including timestamp params.
  for (const m of JSON.stringify(v).matchAll(/\$\{cap\.([A-Za-z0-9_-]+)\}/g)) {
    scope.cap[m[1]] = '2026-01-01T00:00:00Z'
  }

  for (const [i, step] of (v.steps ?? []).entries()) {
    const stepNo = i + 1
    let interpolated
    try {
      interpolated = scope.value(step)
    } catch (err) {
      fail(`step ${stepNo}: ${err.message}`)
      continue
    }
    if (interpolated.$operation) {
      const op = interpolated.$operation
      if (!supported(op.name)) {
        fail(`step ${stepNo}: operation ${op.name} is not supported by @aws-sdk/client-s3`)
      } else {
        try {
          buildInput(op.params ?? {}, (n) => cache.bytes(n))
        } catch (err) {
          fail(`step ${stepNo}: ${err.message}`)
        }
        if (op.presign && !presignSupported(op.name)) {
          fail(`step ${stepNo}: operation ${op.name} cannot be presigned`)
        }
      }
      smokeExpect(op.expect, cache, fail, stepNo)
      smokeCapture(op.capture, fail, stepNo)
    } else if (interpolated.$http) {
      const st = interpolated.$http
      if (st.body !== undefined) {
        try {
          contentValue(st.body, (n) => cache.bytes(n))
        } catch (err) {
          fail(`step ${stepNo}: body: ${err.message}`)
        }
      }
      smokeExpect(st.expect, cache, fail, stepNo)
      smokeCapture(st.capture, fail, stepNo)
    } else {
      fail(`step ${stepNo} has no union key`)
    }
  }
  return problems
}

function smokeCapture (spec, fail, stepNo) {
  for (const [name, path] of Object.entries(spec ?? {})) {
    try {
      parsePath(path)
    } catch (err) {
      fail(`step ${stepNo}: capture ${name}: ${err.message}`)
    }
  }
}

function smokeExpect (exp, cache, fail, stepNo) {
  if (exp == null) return
  compileMatchers(exp.error, fail, stepNo)
  compileMatchers(exp.response, fail, stepNo)
  for (const matcher of Object.values(exp.headers ?? {})) compileMatchers(matcher, fail, stepNo)
  if (exp.body !== undefined) {
    const b = exp.body
    const isDigest = b !== null && typeof b === 'object' && !Array.isArray(b) &&
      ('$size' in b || '$md5' in b || '$sha256' in b)
    if (!isDigest) {
      try {
        contentValue(b, (n) => cache.bytes(n))
      } catch (err) {
        fail(`step ${stepNo}: expect.body: ${err.message}`)
      }
    }
  }
}

function compileMatchers (v, fail, stepNo) {
  if (Array.isArray(v)) {
    for (const e of v) compileMatchers(e, fail, stepNo)
  } else if (v !== null && typeof v === 'object') {
    for (const [k, e] of Object.entries(v)) {
      if (k === '$matches' && typeof e === 'string') {
        try {
          compileRegex(e)
        } catch (err) {
          fail(`step ${stepNo}: $matches ${JSON.stringify(e)}: ${err.message}`)
        }
        continue
      }
      compileMatchers(e, fail, stepNo)
    }
  }
}
