// $operation step execution (SDK dispatch and presigned paths) and
// expectation evaluation shared with $http steps.

import { call } from './dispatch.js'
import { presignAndExecute } from './presign.js'
import { matchValue, matchHeaders, matchError, matchBody, render } from './match.js'
import { getString } from './jsonpath.js'
import { parseXmlError } from './rawhttp.js'
import { runnerFail, trackBucket, trackKey } from './vector.js'
import { IDENTITY_MAIN } from './config.js'

// Statuses whose error code cannot appear on the wire (HEAD responses and
// 304s have no body) mapped to the codes they imply.
const STATUS_IMPLIED_CODES = {
  304: ['NotModified', ''],
  404: ['NotFound', 'NoSuchKey', 'NoSuchBucket'],
  405: ['MethodNotAllowed'],
  412: ['PreconditionFailed', '']
}

export async function runOperationStep (run, src, sr) {
  let op
  try {
    op = run.scope.value(src)
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  const identity = op.identity || IDENTITY_MAIN
  sr.kind = 'operation'
  sr.name = op.name
  sr.identity = identity

  if (op.presign) {
    sr.presigned = true
    return runPresignedStep(run, op, identity, sr)
  }

  let client
  try {
    client = await run.rt.identities.client(identity)
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  let res
  try {
    res = await call(client, op.name, op.params, (n) => run.cache.bytes(n), run.signal)
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  sr.status = res.status
  if (res.err === null) {
    const bucket = typeof op.params?.Bucket === 'string' ? op.params.Bucket : ''
    const key = typeof op.params?.Key === 'string' ? op.params.Key : ''
    if (op.name === 'CreateBucket') trackBucket(run, bucket)
    if (['PutObject', 'CopyObject', 'CompleteMultipartUpload'].includes(op.name)) trackKey(run, bucket, key)
  }
  evaluateOperation(run, op, res, sr)
  if (sr.failures.length > 0 || sr.err !== '') return
  capture(run, op.capture, res.output, sr)
}

// capture evaluates capture paths against a generic value and registers the
// results for later steps.
function capture (run, spec, value, sr) {
  if (!spec || Object.keys(spec).length === 0) return
  sr.captured = {}
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

// evaluateOperation checks an $operation step's expectations against the
// dispatch result.
function evaluateOperation (run, op, res, sr) {
  const exp = op.expect
  const expectsError = exp != null && exp.error !== undefined

  if (expectsError && res.err === null) {
    sr.failures.push({ field: 'error', expected: render(exp.error), actual: `success (status ${res.status})` })
    return
  }
  if (!expectsError && res.err !== null) {
    // A non-2xx expect.status without expect.error (e.g. 304 responses,
    // which surface as SDK errors) is still a pass when the status and
    // remaining assertions hold.
    const statusTolerated = exp != null && exp.status !== undefined && exp.status === res.status
    if (!statusTolerated) {
      sr.failures.push({
        field: 'error',
        expected: 'success',
        actual: `status ${res.status}, error ${res.code}: ${res.msg}`
      })
      return
    }
  }
  if (exp == null) return
  if (exp.status !== undefined && res.status !== exp.status) {
    sr.failures.push({ field: 'status', expected: String(exp.status), actual: String(res.status) })
  }
  if (expectsError) {
    evalError(run, exp.error, res.code, res.msg, res.status, op.name.startsWith('Head'), sr)
  }
  evalHeaders(run, exp.headers, res.headers, sr)
  if (exp.response !== undefined) {
    for (const m of matchValue('', exp.response, res.output, res.output != null)) {
      sr.failures.push({ field: 'response.' + m.path, expected: m.expected, actual: m.actual })
    }
  }
  evalBody(run, exp.body, res.body, sr)
}

function evalError (run, expected, code, msg, status, bodyless, sr) {
  let mismatches
  try {
    mismatches = matchError(expected, code, msg)
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  if (mismatches.length > 0 && (bodyless || status === 304)) {
    // The wire response carried no error document; accept the expected code
    // when the status implies it.
    const want = typeof expected === 'string' ? expected : expected?.code ?? ''
    const implied = STATUS_IMPLIED_CODES[status]
    if (want !== '' && implied && implied.includes(want) && implied.includes(code)) return
  }
  for (const m of mismatches) sr.failures.push({ field: m.path, expected: m.expected, actual: m.actual })
}

function evalHeaders (run, expected, headers, sr) {
  if (!expected || Object.keys(expected).length === 0) return
  let mismatches
  try {
    mismatches = matchHeaders(expected, headers ?? {})
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  for (const m of mismatches) sr.failures.push({ field: m.path, expected: m.expected, actual: m.actual })
}

function evalBody (run, expected, body, sr) {
  if (expected === undefined) return
  let mismatches
  try {
    mismatches = matchBody(expected, body ?? new Uint8Array(0), (n) => run.cache.bytes(n))
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  for (const m of mismatches) sr.failures.push({ field: m.path, expected: m.expected, actual: m.actual })
}

// runPresignedStep mints a presigned URL for the operation and executes it
// with fetch. Expectations are evaluated against the raw response (the
// corpus never asserts `response` on presigned steps).
async function runPresignedStep (run, op, identity, sr) {
  let client
  try {
    client = await run.rt.identities.client(identity)
  } catch (err) {
    return runnerFail(run, sr, err)
  }
  let res
  try {
    res = await presignAndExecute(
      client, op.name, op.params, (n) => run.cache.bytes(n), op.presign.expiresIn ?? 0, run.signal)
  } catch (err) {
    sr.err = `executing presigned request: ${err.message}`
    return
  }
  sr.status = res.status
  evaluateRaw(run, op.expect, res.status, res.headers, res.body, sr)
  if (sr.failures.length > 0 || sr.err !== '') return
  capture(run, op.capture, rawCaptureValue(res.status, res.headers), sr)
}

/**
 * Check expectations for steps executed outside the SDK ($http and
 * presigned): status, headers, XML error body, body bytes.
 */
export function evaluateRaw (run, exp, status, headers, body, sr) {
  const expectsError = exp != null && exp.error !== undefined
  const [code, msg] = parseXmlError(body)

  if (expectsError && status < 400 && code === '') {
    sr.failures.push({ field: 'error', expected: render(exp.error), actual: `success (status ${status})` })
    return
  }
  if (!expectsError && (exp == null || exp.status === undefined) && (status < 200 || status > 299)) {
    sr.failures.push({ field: 'status', expected: '2xx', actual: `${status} ${code} ${msg}` })
    return
  }
  if (exp == null) return
  if (exp.status !== undefined && status !== exp.status) {
    sr.failures.push({ field: 'status', expected: String(exp.status), actual: String(status) })
  }
  if (expectsError) evalError(run, exp.error, code, msg, status, false, sr)
  evalHeaders(run, exp.headers, headers, sr)
  if (exp.response !== undefined) {
    return runnerFail(run, sr, new Error('expect.response is not supported on raw HTTP/presigned steps'))
  }
  evalBody(run, exp.body, body, sr)
}

/**
 * The capture-path root for $http/presigned steps: {status, headers} with
 * lowercased header names (first value).
 */
export function rawCaptureValue (status, headers) {
  return { status, headers: { ...headers } }
}
