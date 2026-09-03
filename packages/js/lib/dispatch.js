// $operation dispatch onto aws-sdk-js-v3: dynamic command lookup, raw
// status/header capture on success AND error paths, generic response
// walking for the matcher engine, and error mapping to {status, code, msg}.

import * as clientS3 from '@aws-sdk/client-s3'
import { buildInput } from './coerce.js'

/** Whether the operation exists as a v3 command (PutBucketLifecycle does not). */
export function supported (name) {
  return typeof clientS3[name + 'Command'] === 'function'
}

export function unsupportedError (name) {
  return new Error(`operation ${name} is not supported by @aws-sdk/client-s3`)
}

/**
 * Execute one operation. Throws only for *runner* problems (unsupported
 * operation, undecodable params); server-side failures are reported inside
 * the result.
 * @param {import('@aws-sdk/client-s3').S3Client} client
 * @param {string} name operation name
 * @param {object} params interpolated vector params
 * @param {(name: string) => Uint8Array} resolveData
 * @param {AbortSignal} [signal]
 * @returns {Promise<{status: number, headers: object, output: unknown,
 *   body: Uint8Array | null, err: Error | null, code: string, msg: string}>}
 */
export async function call (client, name, params, resolveData, signal) {
  const Command = clientS3[name + 'Command']
  if (typeof Command !== 'function') throw unsupportedError(name)
  const { input } = buildInput(params ?? {}, resolveData)
  const cmd = new Command(input)

  // Capture the wire status/headers between the deserializer and the HTTP
  // handler, so they are recorded on success and error paths alike.
  const rc = { status: 0, headers: {} }
  cmd.middlewareStack.addRelativeTo((next) => async (args) => {
    const result = await next(args)
    if (result.response && typeof result.response.statusCode === 'number') {
      rc.status = result.response.statusCode
      rc.headers = lowerHeaders(result.response.headers)
    }
    return result
  }, { name: 's3testsRawCapture', relation: 'after', toMiddleware: 'deserializerMiddleware' })

  const res = { status: 0, headers: {}, output: null, body: null, err: null, code: '', msg: '' }
  try {
    const output = await client.send(cmd, { abortSignal: signal })
    res.status = rc.status
    res.headers = rc.headers
    res.output = await walkOutput(output, res)
  } catch (err) {
    res.status = rc.status || err?.$metadata?.httpStatusCode || 0
    res.headers = Object.keys(rc.headers).length ? rc.headers : lowerHeaders(err?.$response?.headers)
    res.err = err
    const mapped = mapError(err)
    res.code = mapped.code
    res.msg = mapped.msg
  }
  return res
}

function lowerHeaders (headers) {
  const out = {}
  for (const [k, v] of Object.entries(headers ?? {})) out[k.toLowerCase()] = v
  return out
}

// mapError extracts the S3 error code/message from a smithy-js exception.
// Bodyless statuses surface synthesized names ("NotFound", numeric strings,
// bare "Error") which are normalized so the shared status→code fallback
// table applies identically to Go.
export function mapError (err) {
  let code = typeof err?.name === 'string' ? err.name : ''
  if (code === 'Error' || code === 'TypeError' || /^\d+$/.test(code)) code = ''
  const msg = typeof err?.message === 'string' ? err.message : String(err)
  return { code, msg }
}

// walkOutput converts a command output into a generic JSON-like value for
// the matcher engine: $metadata dropped, streaming bodies drained into
// res.body and excluded, Dates rendered like Go's RFC3339Nano, binary as
// base64, null/undefined members skipped (so {"$absent": true} works).
async function walkOutput (v, res) {
  if (v === null || v === undefined) return undefined
  if (v instanceof Date) return goRFC3339Nano(v)
  if (v instanceof Uint8Array) return Buffer.from(v).toString('base64')
  if (typeof v?.transformToByteArray === 'function') {
    res.body = await v.transformToByteArray()
    return undefined
  }
  if (Array.isArray(v)) {
    const out = []
    for (const e of v) out.push(await walkOutput(e, res))
    return out
  }
  if (typeof v === 'object') {
    const out = {}
    for (const [k, e] of Object.entries(v)) {
      if (k === '$metadata') continue
      const w = await walkOutput(e, res)
      if (w !== undefined) out[k] = w
    }
    return out
  }
  return v
}

/**
 * Render a Date exactly like Go's time.RFC3339Nano formatting of a UTC time:
 * fractional seconds trimmed of trailing zeros and omitted when zero — so
 * captured values round-trip through parseTime and match Go's rendering.
 * @param {Date} d
 */
export function goRFC3339Nano (d) {
  const pad = (n, w = 2) => String(n).padStart(w, '0')
  let s = `${pad(d.getUTCFullYear(), 4)}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}` +
    `T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`
  const ms = d.getUTCMilliseconds()
  if (ms !== 0) s += '.' + pad(ms, 3).replace(/0+$/, '')
  return s + 'Z'
}
