// Matcher engine implementing the vector matcher semantics: scalar equality,
// recursive subset objects, exact-length ordered arrays, assertion objects
// ($exists/$absent/$eq/$ne/$matches/$length/$contains), plus the header,
// error and body (content-descriptor / digest) expectation forms.

import { createHash } from 'node:crypto'

const ABSENT = Symbol('absent')

/**
 * Match a decoded expected matcher against an actual value.
 * present=false means the addressed value does not exist in the response.
 * @returns {Array<{path: string, expected: string, actual: string}>} mismatches
 */
export function matchValue (path, expected, actual, present) {
  if (isPlainObject(expected)) {
    if (isAssertion(expected)) return assertion(path, expected, actual, present)
    if (!present) return miss(path, expected, ABSENT)
    if (!isPlainObject(actual)) return miss(path, expected, actual)
    const out = []
    for (const [k, ev] of Object.entries(expected)) {
      const has = actual !== null && k in actual && actual[k] !== undefined && actual[k] !== null
      out.push(...matchValue(join(path, k), ev, has ? actual[k] : undefined, has))
    }
    return out
  }
  if (Array.isArray(expected)) {
    if (!present) return miss(path, expected, ABSENT)
    if (!Array.isArray(actual)) return miss(path, expected, actual)
    if (expected.length !== actual.length) {
      return [{ path, expected: `array of length ${expected.length}`, actual: `array of length ${actual.length}` }]
    }
    const out = []
    for (let i = 0; i < expected.length; i++) {
      out.push(...matchValue(`${path}[${i}]`, expected[i], actual[i], true))
    }
    return out
  }
  // Scalar literal: exact equality.
  if (!present) return miss(path, expected, ABSENT)
  if (!scalarEqual(expected, actual)) return miss(path, expected, actual)
  return []
}

function isPlainObject (v) {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

function join (path, key) {
  return path === '' ? key : path + '.' + key
}

function isAssertion (m) {
  const keys = Object.keys(m)
  return keys.length > 0 && keys.every((k) => k.startsWith('$'))
}

function assertion (path, m, actual, present) {
  const out = []
  for (const [op, arg] of Object.entries(m)) {
    switch (op) {
      case '$exists':
        if (present !== Boolean(arg)) {
          out.push({ path, expected: `exists: ${Boolean(arg)}`, actual: presence(actual, present) })
        }
        break
      case '$absent':
        if (present === Boolean(arg)) {
          out.push({ path, expected: `absent: ${Boolean(arg)}`, actual: presence(actual, present) })
        }
        break
      case '$eq':
        if (!present || !literalEqual(arg, actual)) out.push(...miss(path, arg, present ? actual : ABSENT))
        break
      case '$ne':
        // Scalar inequality; an absent value fails (the assertion demands a
        // differing value).
        if (!present || scalarEqual(arg, actual)) {
          out.push({ path, expected: 'not equal to ' + render(arg), actual: presence(actual, present) })
        }
        break
      case '$matches': {
        const pat = typeof arg === 'string' ? arg : ''
        const s = scalarString(actual)
        if (!present || s === undefined) {
          out.push({ path, expected: 'matches ' + JSON.stringify(pat), actual: presence(actual, present) })
          break
        }
        let matched
        try {
          matched = regex(pat).test(s)
        } catch (err) {
          out.push({ path, expected: 'matches ' + JSON.stringify(pat), actual: 'invalid regex: ' + err.message })
          break
        }
        if (!matched) out.push({ path, expected: 'matches ' + JSON.stringify(pat), actual: JSON.stringify(s) })
        break
      }
      case '$length': {
        const n = lengthOf(actual)
        if (!present || typeof arg !== 'number' || n === undefined || n !== arg) {
          out.push({ path, expected: `length ${arg}`, actual: lengthActual(actual, present) })
        }
        break
      }
      case '$contains': {
        if (!present || !Array.isArray(actual)) {
          out.push({ path, expected: 'array containing ' + render(arg), actual: presence(actual, present) })
          break
        }
        const found = actual.some((el) => matchValue(path, arg, el, true).length === 0)
        if (!found) {
          out.push({ path, expected: 'some element matching ' + render(arg), actual: `no match among ${actual.length} element(s)` })
        }
        break
      }
      default:
        out.push({ path, expected: 'known assertion', actual: 'unknown assertion operator ' + op })
    }
  }
  return out
}

// literalEqual is deep equality with numeric normalization ($eq semantics).
function literalEqual (expected, actual) {
  if (isPlainObject(expected)) {
    if (!isPlainObject(actual)) return false
    const ek = Object.keys(expected)
    if (Object.keys(actual).length !== ek.length) return false
    return ek.every((k) => k in actual && literalEqual(expected[k], actual[k]))
  }
  if (Array.isArray(expected)) {
    return Array.isArray(actual) && actual.length === expected.length &&
      expected.every((e, i) => literalEqual(e, actual[i]))
  }
  return scalarEqual(expected, actual)
}

function scalarEqual (expected, actual) {
  if (typeof expected === 'number') return typeof actual === 'number' && expected === actual
  if (typeof expected === 'string') return actual === expected
  if (typeof expected === 'boolean') return actual === expected
  if (expected === null) return actual === null || actual === undefined
  return false
}

function scalarString (v) {
  switch (typeof v) {
    case 'string': return v
    case 'number': return String(v)
    case 'boolean': return String(v)
    default: return undefined
  }
}

function lengthOf (v) {
  if (Array.isArray(v) || typeof v === 'string') return v.length
  return undefined
}

function lengthActual (v, present) {
  if (!present) return '(absent)'
  const n = lengthOf(v)
  return n === undefined ? `${typeof v} (no length)` : `length ${n}`
}

function presence (v, present) {
  return present ? render(v) : '(absent)'
}

function miss (path, expected, actual) {
  return [{ path, expected: render(expected), actual: actual === ABSENT ? '(absent)' : render(actual) }]
}

/** Render a value compactly for mismatch output (mirrors the Go renderer). */
export function render (v) {
  let s
  try {
    s = JSON.stringify(v)
  } catch {
    s = String(v)
  }
  if (s === undefined) s = String(v)
  if (s.length > 256) s = s.slice(0, 256) + '…'
  return s
}

// $matches patterns are the portable ECMA-262 ∩ RE2 subset per the spec, so
// native RegExp compiles them; matching is unanchored.
const regexCache = new Map()
function regex (pattern) {
  let re = regexCache.get(pattern)
  if (!re) {
    re = new RegExp(pattern)
    regexCache.set(pattern, re)
  }
  return re
}

/** compileRegex reports template validity for the offline corpus smoke test. */
export function compileRegex (pattern) {
  new RegExp(pattern) // eslint-disable-line no-new
}

/**
 * Match expect.headers (lowercase name -> matcher) against actual response
 * headers (a lowercase-keyed object of first values).
 */
export function matchHeaders (expected, headers) {
  const out = []
  for (const [name, matcher] of Object.entries(expected)) {
    const present = name in headers
    out.push(...matchValue('headers.' + name, matcher, present ? headers[name] : undefined, present))
  }
  return out
}

/**
 * Match expect.error (a code string, or {code, message}) against the
 * observed error code/message.
 */
export function matchError (expected, code, message) {
  if (typeof expected === 'string') {
    if (code !== expected) return [{ path: 'error', expected, actual: orEmpty(code) }]
    return []
  }
  if (isPlainObject(expected)) {
    const out = []
    if (typeof expected.code === 'string' && code !== expected.code) {
      out.push({ path: 'error.code', expected: expected.code, actual: orEmpty(code) })
    }
    if ('message' in expected) {
      out.push(...matchValue('error.message', expected.message, message, message !== ''))
    }
    return out
  }
  throw new Error('expect.error: unsupported form ' + render(expected))
}

/**
 * Match expect.body — either a content descriptor (exact bytes) or a digest
 * assertion {$size,$md5,$sha256} — against the actual body bytes.
 * @param {unknown} expected decoded expect.body value
 * @param {Uint8Array} body
 * @param {(name: string) => Uint8Array} resolve
 */
export function matchBody (expected, body, resolve) {
  if (isPlainObject(expected) && isAssertion(expected) && !('$data' in expected) && !('$base64' in expected)) {
    return digestBody(expected, body)
  }
  const want = contentValue(expected, resolve)
  if (!buffersEqual(want, body)) {
    return [{ path: 'body', expected: summarize(want), actual: summarize(body) }]
  }
  return []
}

function digestBody (m, body) {
  const out = []
  for (const [op, arg] of Object.entries(m)) {
    switch (op) {
      case '$size':
        if (arg !== body.length) out.push({ path: 'body.$size', expected: render(arg), actual: String(body.length) })
        break
      case '$md5': {
        const got = createHash('md5').update(body).digest('hex')
        if (got !== arg) out.push({ path: 'body.$md5', expected: render(arg), actual: got })
        break
      }
      case '$sha256': {
        const got = createHash('sha256').update(body).digest('hex')
        if (got !== arg) out.push({ path: 'body.$sha256', expected: render(arg), actual: got })
        break
      }
      default:
        throw new Error('expect.body: unknown digest assertion ' + op)
    }
  }
  return out
}

/**
 * Decode a content descriptor — plain string (UTF-8), {"$data": name} or
 * {"$base64": "..."} — into bytes.
 * @param {unknown} v
 * @param {(name: string) => Uint8Array} resolve
 * @returns {Uint8Array}
 */
export function contentValue (v, resolve) {
  if (typeof v === 'string') return new TextEncoder().encode(v)
  if (isPlainObject(v)) {
    const keys = Object.keys(v)
    if (keys.length === 1 && typeof v.$data === 'string') {
      if (!resolve) throw new Error(`content descriptor references dataset ${JSON.stringify(v.$data)} but the vector declares no data`)
      return resolve(v.$data)
    }
    if (keys.length === 1 && typeof v.$base64 === 'string') {
      try {
        return Uint8Array.from(Buffer.from(v.$base64, 'base64'))
      } catch (err) {
        throw new Error('bad $base64 content: ' + err.message)
      }
    }
  }
  throw new Error('invalid content descriptor: ' + render(v))
}

function buffersEqual (a, b) {
  return a.length === b.length && Buffer.compare(Buffer.from(a), Buffer.from(b)) === 0
}

function summarize (b) {
  const md5 = createHash('md5').update(b).digest('hex')
  let s = `${b.length} bytes, md5 ${md5}`
  if (b.length <= 64 && [...b].every((c) => c >= 0x20 && c <= 0x7e)) {
    s += ` (${JSON.stringify(new TextDecoder().decode(b))})`
  }
  return s
}

function orEmpty (s) {
  return s === '' ? '(no error)' : s
}
