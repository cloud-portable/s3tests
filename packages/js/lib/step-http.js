// $http step execution over raw sockets.

import { rawRequest } from './rawhttp.js'
import { contentValue } from './match.js'
import { runnerFail, trackBucket } from './vector.js'
import { evaluateRaw, rawCaptureValue } from './step-operation.js'
import { IDENTITY_MAIN, IDENTITY_ANONYMOUS } from './config.js'
import { getString } from './jsonpath.js'

export async function runHttpStep (run, src, sr) {
  let st
  try {
    st = run.scope.value(src)
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  const identity = st.identity || IDENTITY_MAIN
  sr.kind = 'http'
  sr.name = `${st.method} ${st.path}`
  sr.identity = identity

  // sign defaults to true; anonymous requests are inherently unsigned.
  const sign = (st.sign ?? true) && identity !== IDENTITY_ANONYMOUS
  let credentials = null
  if (sign) {
    try {
      credentials = await run.rt.identities.resolveCredentials(identity)
    } catch (err) {
      return runnerFail(run, sr, err)
    }
  }

  let body = new Uint8Array(0)
  if (st.body !== undefined) {
    try {
      body = contentValue(st.body, (n) => run.cache.bytes(n))
    } catch (err) {
      return runnerFail(run, sr, new Error('body: ' + err.message))
    }
  }

  let res
  try {
    res = await rawRequest(run.rt.cfg.endpoint, {
      method: st.method,
      path: st.path,
      query: normalizeMulti(st.query),
      headers: orderedHeaders(st.headers),
      body,
      sign,
      credentials,
      region: run.rt.cfg.region
    }, run.signal)
  } catch (err) {
    // A transport-level failure is an observation about the server, not a
    // runner bug: report it as the step's failure.
    sr.err = err.message
    return
  }
  sr.status = res.status
  // A successful raw PUT on a bare bucket path is a bucket creation the
  // teardown must cover.
  if (st.method === 'PUT' && res.status >= 200 && res.status < 300 &&
      Object.keys(st.query ?? {}).length === 0) {
    const seg = st.path.replace(/^\/+|\/+$/g, '')
    if (seg !== '' && !seg.includes('/')) trackBucket(run, seg)
  }
  evaluateRaw(run, st.expect, res.status, res.headers, res.body, sr)
  if (sr.failures.length > 0 || sr.err !== '') return
  captureRaw(run, st.capture, res, sr)
}

function captureRaw (run, spec, res, sr) {
  if (!spec || Object.keys(spec).length === 0) return
  sr.captured = {}
  const value = rawCaptureValue(res.status, res.headers)
  for (const [name, path] of Object.entries(spec)) {
    let val
    try {
      val = getString(value, path)
    } catch (err) {
      sr.err = `capture ${name}: ${err.message}`
      return
    }
    sr.captured[name] = val
    run.scope.cap[name] = val
  }
}

/** Normalize OneOrMany (string | string[]) maps into string[] values. */
function normalizeMulti (m) {
  if (!m) return {}
  const out = {}
  for (const [k, v] of Object.entries(m)) out[k] = Array.isArray(v) ? v : [v]
  return out
}

/**
 * Flatten the step's header map into a deterministic ordered list (JSON
 * objects carry no order, so keys are sorted; multi-valued headers keep
 * their declared value order).
 */
function orderedHeaders (m) {
  if (!m) return []
  const out = []
  for (const name of Object.keys(m).sort()) {
    const values = Array.isArray(m[name]) ? m[name] : [m[name]]
    for (const v of values) out.push([name, v])
  }
  return out
}
