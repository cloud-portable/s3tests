// Vector params (interpolated plain JSON, AWS API-model member names) →
// aws-sdk-js-v3 command inputs. v3 has no runtime shape metadata, so the
// conversions the SDK's serializers require are driven by explicit,
// corpus-derived key sets; the offline corpus smoke test is the drift alarm
// when a corpus bump introduces new shapes.

import { contentValue } from './match.js'

// Fields (at any depth) whose values are timestamps the serializers need as
// Date instances.
const TIMESTAMP_KEYS = new Set([
  'CopySourceIfModifiedSince', 'CopySourceIfUnmodifiedSince',
  'IfModifiedSince', 'IfUnmodifiedSince',
  'IfMatchLastModifiedTime', 'Expires',
  'ObjectLockRetainUntilDate', 'RetainUntilDate',
  'LastModifiedTime',
  'Date'
])

// Fields modeled as strings that the corpus writes as bare numbers.
const STRINGIFY_KEYS = new Set(['PartNumberMarker'])

/**
 * Build a v3 command input from interpolated vector params.
 * @param {object} params
 * @param {(name: string) => Uint8Array} resolveData $data resolver
 * @returns {{input: object, body: Uint8Array | null}} body = held-aside Body
 *   bytes (the presign path sends them itself)
 */
export function buildInput (params, resolveData) {
  let body = null
  const walk = (value, key) => {
    if (key === 'Body') {
      body = contentValue(value, resolveData)
      return body
    }
    if (typeof value === 'string') {
      if (TIMESTAMP_KEYS.has(key)) return parseTime(value)
      return value
    }
    if (typeof value === 'number' && STRINGIFY_KEYS.has(key)) return String(value)
    if (Array.isArray(value)) return value.map((e) => walk(e, key))
    if (value !== null && typeof value === 'object') {
      // Object-valued string params: boto3-style CopySource and binary
      // content descriptors (SSE-C keys), whose wire form is base64.
      if (key === 'CopySource' && typeof value.Bucket === 'string' && typeof value.Key === 'string') {
        let src = value.Bucket + '/' + value.Key
        if (typeof value.VersionId === 'string' && value.VersionId !== '') src += '?versionId=' + value.VersionId
        return src
      }
      if (('$base64' in value || '$data' in value) && Object.keys(value).length === 1) {
        return Buffer.from(contentValue(value, resolveData)).toString('base64')
      }
      const out = {}
      for (const [k, v] of Object.entries(value)) out[k] = walk(v, k)
      return out
    }
    return value
  }
  const input = {}
  for (const [k, v] of Object.entries(params)) input[k] = walk(v, k)
  return { input, body }
}

const RFC3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$/
const HTTP_DATE = /^[A-Z][a-z]{2}, \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} GMT$/
const BARE_DATE = /^(\d{4})-(\d{2})-(\d{2})$/
const COMPACT_DATE = /^(\d{4})(\d{2})(\d{2})$/

/**
 * Parse the corpus's timestamp shapes: RFC3339 (the norm), HTTP-date
 * ("Expires" params) and bare dates (lifecycle rules). Throws on anything
 * else (a vector-definition error → RunnerError).
 * @param {string} s
 * @returns {Date}
 */
export function parseTime (s) {
  let m
  if (RFC3339.test(s) || HTTP_DATE.test(s)) {
    const d = new Date(s)
    if (!Number.isNaN(d.getTime())) return d
  } else if ((m = s.match(BARE_DATE)) || (m = s.match(COMPACT_DATE))) {
    return new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])))
  }
  throw new Error(`unparseable timestamp ${JSON.stringify(s)}`)
}
