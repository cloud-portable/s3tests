// $http steps over raw TCP/TLS sockets. The corpus's wire-level tests send
// headers no HTTP client library will emit (empty authorization,
// content-length "" or "-1"), so requests are serialized by hand and
// responses parsed with a minimal reader.

import net from 'node:net'
import tls from 'node:tls'
import { createHash } from 'node:crypto'
import { SignatureV4 } from '@smithy/signature-v4'
import { HttpRequest } from '@smithy/protocol-http'
import { Sha256 } from '@aws-crypto/sha256-js'

/**
 * Assemble, optionally sign, send the request to the endpoint and read the
 * response.
 * @param {string} endpoint http(s)://host[:port]
 * @param {{method: string, path: string, query?: Record<string, string[]>,
 *   headers?: Array<[string, string]>, body?: Uint8Array, sign: boolean,
 *   credentials?: {accessKeyId: string, secretAccessKey: string, sessionToken?: string},
 *   region?: string}} req headers are ordered step overrides
 * @param {AbortSignal} [signal]
 * @returns {Promise<{status: number, headers: Record<string, string>, body: Uint8Array}>}
 */
export async function rawRequest (endpoint, req, signal) {
  const u = new URL(endpoint)
  const body = req.body ?? new Uint8Array(0)
  let target = req.path
  const query = encodeQuery(req.query)
  if (query !== '') target += '?' + query

  // Defaults, later overridden by step headers (case-insensitively; an
  // override applies even with an empty value — that is the point of the
  // wire-header tests).
  let headers = [
    ['Host', u.host],
    ['Content-Length', String(body.length)],
    ['Connection', 'close']
  ]
  if (req.sign) {
    headers.push(...await signHeaders(u.host, target, req, body))
  }
  headers = applyOverrides(headers, req.headers ?? [])

  const raw = await send(u, req.method, target, headers, body, signal)
  return parseResponse(raw)
}

function encodeQuery (query) {
  if (!query) return ''
  const parts = []
  for (const k of Object.keys(query).sort()) {
    for (const v of query[k]) parts.push(k + '=' + v) // values sent as given
  }
  return parts.join('&')
}

// signHeaders SigV4-signs a shadow request carrying the step headers (so the
// signature covers them) and returns the resulting auth headers.
// Content-Length is deliberately never signed: SigV4 does not require it,
// and the corpus overrides it on requests that must still authenticate.
async function signHeaders (host, target, req, body) {
  const [rawPath, rawQuery = ''] = splitTarget(target)
  let payloadHash = createHash('sha256').update(body).digest('hex')
  const shadowHeaders = { host }
  for (const [name, value] of req.headers ?? []) {
    const lower = name.toLowerCase()
    if (lower === 'content-length') continue // wire-only; never signed
    if (lower === 'x-amz-content-sha256') payloadHash = value
    shadowHeaders[lower] = value
  }
  shadowHeaders['x-amz-content-sha256'] = payloadHash

  const query = {}
  if (rawQuery !== '') {
    for (const pair of rawQuery.split('&')) {
      const eq = pair.indexOf('=')
      const k = eq < 0 ? pair : pair.slice(0, eq)
      const v = eq < 0 ? '' : pair.slice(eq + 1)
      if (k in query) {
        query[k] = [].concat(query[k], v)
      } else {
        query[k] = v
      }
    }
  }

  const signer = new SignatureV4({
    service: 's3',
    region: req.region ?? 'us-east-1',
    credentials: req.credentials,
    sha256: Sha256,
    uriEscapePath: false, // S3: paths are canonicalized as-is
    applyChecksum: false // we set x-amz-content-sha256 ourselves
  })
  const signed = await signer.sign(new HttpRequest({
    method: req.method,
    protocol: 'http:',
    hostname: host,
    path: rawPath,
    query,
    headers: shadowHeaders,
    body
  }))

  const out = []
  for (const name of ['X-Amz-Date', 'X-Amz-Content-Sha256', 'X-Amz-Security-Token', 'Authorization']) {
    const v = signed.headers[name.toLowerCase()]
    if (v !== undefined && v !== '') out.push([name, v])
  }
  return out
}

function splitTarget (target) {
  const q = target.indexOf('?')
  return q < 0 ? [target, ''] : [target.slice(0, q), target.slice(q + 1)]
}

function applyOverrides (base, overrides) {
  const out = base.map((h) => [...h])
  for (const [name, value] of overrides) {
    const i = out.findIndex(([n]) => n.toLowerCase() === name.toLowerCase())
    if (i >= 0) {
      out[i] = [name, value]
    } else {
      out.push([name, value])
    }
  }
  return out
}

function send (u, method, target, headers, body, signal) {
  return new Promise((resolve, reject) => {
    const port = u.port !== '' ? Number(u.port) : (u.protocol === 'https:' ? 443 : 80)
    const socket = u.protocol === 'https:'
      ? tls.connect({ host: u.hostname, port, servername: u.hostname })
      : net.connect({ host: u.hostname, port })
    const chunks = []
    let settled = false
    const fail = (err) => {
      if (settled) return
      settled = true
      socket.destroy()
      reject(err)
    }
    const onAbort = () => fail(new Error('request aborted'))
    signal?.addEventListener('abort', onAbort, { once: true })
    socket.setTimeout(60_000, () => fail(new Error('socket timeout')))
    socket.on('error', fail)
    socket.on(u.protocol === 'https:' ? 'secureConnect' : 'connect', () => {
      let head = `${method} ${target} HTTP/1.1\r\n`
      for (const [name, value] of headers) head += `${name}: ${value}\r\n`
      head += '\r\n'
      socket.write(head)
      if (body.length > 0) socket.write(body)
    })
    socket.on('data', (c) => chunks.push(c))
    socket.on('close', () => {
      if (settled) return
      settled = true
      signal?.removeEventListener('abort', onAbort)
      resolve(Buffer.concat(chunks))
    })
  })
}

// parseResponse is a minimal HTTP/1.1 response reader: status line, headers
// (lowercase, first value wins), then a body framed by Transfer-Encoding:
// chunked, Content-Length, or read-to-close (we send Connection: close).
export function parseResponse (raw) {
  const sep = raw.indexOf('\r\n\r\n')
  if (sep < 0) throw new Error('malformed HTTP response: no header terminator')
  const headText = raw.subarray(0, sep).toString('latin1')
  const lines = headText.split('\r\n')
  const statusMatch = lines[0].match(/^HTTP\/\d\.\d (\d{3})/)
  if (!statusMatch) throw new Error('malformed HTTP status line: ' + lines[0])
  const status = Number(statusMatch[1])
  const headers = {}
  for (const line of lines.slice(1)) {
    const colon = line.indexOf(':')
    if (colon < 0) continue
    const name = line.slice(0, colon).trim().toLowerCase()
    const value = line.slice(colon + 1).trim()
    if (!(name in headers)) headers[name] = value
  }
  let body = raw.subarray(sep + 4)
  if ((headers['transfer-encoding'] ?? '').toLowerCase().includes('chunked')) {
    body = dechunk(body)
  } else if ('content-length' in headers) {
    const n = Number(headers['content-length'])
    if (Number.isInteger(n) && n >= 0) body = body.subarray(0, n)
  }
  return { status, headers, body: new Uint8Array(body) }
}

function dechunk (buf) {
  const out = []
  let i = 0
  while (i < buf.length) {
    const lineEnd = buf.indexOf('\r\n', i)
    if (lineEnd < 0) break
    const size = parseInt(buf.subarray(i, lineEnd).toString('latin1').split(';')[0], 16)
    if (!Number.isInteger(size)) break
    if (size === 0) break
    out.push(buf.subarray(lineEnd + 2, lineEnd + 2 + size))
    i = lineEnd + 2 + size + 2 // skip trailing CRLF
  }
  return Buffer.concat(out)
}

/**
 * Extract <Error><Code>/<Message> from an S3 XML error body; empty strings
 * when the body is not an XML error document.
 * @param {Uint8Array} body
 */
export function parseXmlError (body) {
  const text = Buffer.from(body).toString('utf8')
  if (!/<Error[\s>]/.test(text)) return ['', '']
  const pick = (tag) => {
    const m = text.match(new RegExp(`<${tag}>([^<]*)</${tag}>`))
    return m ? decodeEntities(m[1]) : ''
  }
  return [pick('Code'), pick('Message')]
}

function decodeEntities (s) {
  return s.replace(/&(amp|lt|gt|quot|#39|apos);/g, (m, e) => ({
    amp: '&', lt: '<', gt: '>', quot: '"', '#39': "'", apos: "'"
  })[e])
}
