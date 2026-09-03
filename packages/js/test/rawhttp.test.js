import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { once } from 'node:events'
import { rawRequest, parseResponse, parseXmlError } from '../lib/rawhttp.js'

const OK_RESPONSE = 'HTTP/1.1 200 OK\r\nx-amz-request-id: R1\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok'

/** Raw TCP server: captures the request head, replies with `response`. */
async function rawServer (response) {
  let captured = ''
  const server = net.createServer((socket) => {
    socket.on('data', (chunk) => {
      captured += chunk.toString('latin1')
      if (captured.includes('\r\n\r\n')) {
        socket.end(response)
      }
    })
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    head: () => captured,
    close: () => new Promise((r) => server.close(r))
  }
}

const creds = { accessKeyId: 'AK', secretAccessKey: 'SK' }

test('unsigned requests send literal bytes with no auth headers', async () => {
  const srv = await rawServer(OK_RESPONSE)
  try {
    const res = await rawRequest(srv.url, {
      method: 'PUT',
      path: '/bucket/k',
      query: { partNumber: ['1'] },
      headers: [['content-length', '-1'], ['x-weird', '']],
      body: new TextEncoder().encode('abc'),
      sign: false
    })
    assert.equal(res.status, 200)
    assert.equal(res.headers['x-amz-request-id'], 'R1')
    assert.equal(Buffer.from(res.body).toString(), 'ok')
    const head = srv.head()
    assert.ok(head.startsWith('PUT /bucket/k?partNumber=1 HTTP/1.1\r\n'), head)
    assert.ok(head.includes('content-length: -1\r\n'), 'content-length override sent literally')
    assert.ok(head.includes('x-weird: \r\n'), 'empty header value sent')
    assert.ok(!/authorization|x-amz-date/i.test(head), 'no auth headers when unsigned')
  } finally {
    await srv.close()
  }
})

test('signed requests cover step headers but never content-length', async () => {
  const srv = await rawServer(OK_RESPONSE)
  try {
    await rawRequest(srv.url, {
      method: 'PUT',
      path: '/bucket/key',
      headers: [['content-md5', 'rL0Y20xC+Fzt72VPzMSk2A==']],
      body: new TextEncoder().encode('da'),
      sign: true,
      credentials: creds,
      region: 'us-east-1'
    })
    const head = srv.head()
    assert.ok(head.includes('Authorization: AWS4-HMAC-SHA256 Credential=AK/'), head)
    const authLine = head.split('\r\n').find((l) => l.startsWith('Authorization:')).toLowerCase()
    assert.ok(authLine.includes('content-md5'), 'content-md5 must be in SignedHeaders')
    assert.ok(!authLine.includes('content-length'), 'content-length must not be signed')
    assert.ok(head.includes('X-Amz-Content-Sha256: '), 'payload hash header present')
    assert.ok(head.includes('Content-Length: 2\r\n'), 'default content-length sent')
  } finally {
    await srv.close()
  }
})

test('authorization override wins over the signed value', async () => {
  const srv = await rawServer(OK_RESPONSE)
  try {
    await rawRequest(srv.url, {
      method: 'GET',
      path: '/bucket',
      headers: [['authorization', '']],
      sign: true,
      credentials: creds,
      region: 'us-east-1'
    })
    const head = srv.head()
    assert.ok(head.includes('authorization: \r\n'), 'override must win')
    assert.ok(!head.includes('AWS4-HMAC-SHA256'), 'signed value must not leak')
  } finally {
    await srv.close()
  }
})

test('response parsing: content-length, chunked, read-to-close', () => {
  const cl = parseResponse(Buffer.from('HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhelloEXTRA'))
  assert.equal(Buffer.from(cl.body).toString(), 'hello')

  const chunked = parseResponse(Buffer.from(
    'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n'))
  assert.equal(Buffer.from(chunked.body).toString(), 'hello world')

  const close = parseResponse(Buffer.from('HTTP/1.1 404 Not Found\r\nX: y\r\n\r\neverything until close'))
  assert.equal(close.status, 404)
  assert.equal(Buffer.from(close.body).toString(), 'everything until close')
})

test('parseXmlError', () => {
  const [code, msg] = parseXmlError(new TextEncoder().encode(
    '<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>it&apos;s gone &amp; lost</Message></Error>'))
  assert.equal(code, 'NoSuchKey')
  assert.equal(msg, "it's gone & lost")
  assert.deepEqual(parseXmlError(new TextEncoder().encode('not xml')), ['', ''])
  assert.deepEqual(parseXmlError(new Uint8Array(0)), ['', ''])
})
