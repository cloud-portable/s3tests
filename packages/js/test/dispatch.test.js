import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { call, supported, goRFC3339Nano } from '../lib/dispatch.js'
import { withDefaults, buildClient, IDENTITY_ANONYMOUS, Identities } from '../lib/config.js'

/** Start a canned-response server; returns {url, requests, close}. */
async function serve (handler) {
  const requests = []
  const server = http.createServer((req, res) => {
    let body = ''
    req.on('data', (c) => { body += c })
    req.on('end', () => {
      requests.push({ method: req.method, url: req.url, headers: req.headers, body })
      handler(req, res)
    })
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    requests,
    close: () => new Promise((r) => server.close(r))
  }
}

const client = (url) => buildClient(
  withDefaults({ endpoint: url, credentials: { accessKeyId: 'AK', secretAccessKey: 'SK' } }),
  { accessKeyId: 'AK', secretAccessKey: 'SK' })

test('supported', () => {
  for (const op of ['GetObject', 'PutObject', 'CreateMultipartUpload', 'ListObjectsV2', 'DeleteBucket']) {
    assert.ok(supported(op), op)
  }
  assert.ok(!supported('PutBucketLifecycle'), 'PutBucketLifecycle dropped from aws-sdk-js-v3')
})

test('success path captures raw status/headers and walks output', async () => {
  const srv = await serve((req, res) => {
    res.setHeader('x-amz-request-id', 'REQ1')
    res.writeHead(200, { 'content-type': 'application/xml' })
    res.end('<?xml version="1.0"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>UP123</UploadId></InitiateMultipartUploadResult>')
  })
  try {
    const res = await call(client(srv.url), 'CreateMultipartUpload', { Bucket: 'b', Key: 'k' }, null)
    assert.equal(res.err, null)
    assert.equal(res.status, 200)
    assert.equal(res.headers['x-amz-request-id'], 'REQ1')
    assert.equal(res.output.UploadId, 'UP123')
    assert.ok(!('$metadata' in res.output))
  } finally {
    await srv.close()
  }
})

test('error path maps code/message and keeps raw status', async () => {
  const srv = await serve((req, res) => {
    res.writeHead(404, { 'content-type': 'application/xml' })
    res.end('<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>')
  })
  try {
    const res = await call(client(srv.url), 'GetObject', { Bucket: 'b', Key: 'missing' }, null)
    assert.notEqual(res.err, null)
    assert.equal(res.status, 404)
    assert.equal(res.code, 'NoSuchKey')
  } finally {
    await srv.close()
  }
})

test('GetObject drains the streaming body and renders Go-style dates', async () => {
  const srv = await serve((req, res) => {
    res.writeHead(200, {
      'content-type': 'text/plain',
      'last-modified': 'Mon, 02 Jan 2006 15:04:05 GMT',
      'content-length': '10'
    })
    res.end('hello body')
  })
  try {
    const res = await call(client(srv.url), 'GetObject', { Bucket: 'b', Key: 'k' }, null)
    assert.equal(res.err, null)
    assert.equal(Buffer.from(res.body).toString(), 'hello body')
    assert.ok(!('Body' in res.output), 'stream excluded from the generic value')
    assert.equal(res.output.ContentLength, 10)
    assert.equal(res.output.LastModified, '2006-01-02T15:04:05Z')
  } finally {
    await srv.close()
  }
})

test('wire discipline: single attempt, no Expect header, no implicit checksums, verbatim SSE-C', async () => {
  const srv = await serve((req, res) => {
    res.writeHead(500, { 'content-type': 'application/xml' })
    res.end('<?xml version="1.0"?><Error><Code>Boom</Code><Message>x</Message></Error>')
  })
  try {
    const key = 'pO3upElrwuEXSoFwCfnZPdSsmt/xWeFa0N9KgDijwVs='
    await call(client(srv.url), 'PutObject',
      { Bucket: 'b', Key: 'k', Body: 'data', SSECustomerKey: key, SSECustomerAlgorithm: 'AES256', SSECustomerKeyMD5: 'md5md5md5md5md5md5md5w==' }, null)
    assert.equal(srv.requests.length, 1, 'retries must be off')
    const h = srv.requests[0].headers
    assert.ok(!('expect' in h), 'Expect: 100-continue must be disabled')
    assert.ok(!('x-amz-checksum-crc32' in h) && !('x-amz-sdk-checksum-algorithm' in h), 'no implicit checksums')
    assert.equal(h['x-amz-server-side-encryption-customer-key'], key, 'SSE-C key sent verbatim')
  } finally {
    await srv.close()
  }
})

test('anonymous identity sends no auth headers', async () => {
  const srv = await serve((req, res) => {
    res.writeHead(200, { 'content-type': 'application/xml' })
    res.end('<?xml version="1.0"?><ListAllMyBucketsResult></ListAllMyBucketsResult>')
  })
  try {
    const ids = new Identities(withDefaults({ endpoint: srv.url, credentials: { accessKeyId: 'AK', secretAccessKey: 'SK' } }))
    const anon = await ids.client(IDENTITY_ANONYMOUS)
    await call(anon, 'ListBuckets', {}, null)
    const h = srv.requests[0].headers
    assert.ok(!('authorization' in h), 'anonymous request must be unsigned: ' + JSON.stringify(Object.keys(h)))
  } finally {
    await srv.close()
  }
})

test('goRFC3339Nano matches Go formatting', () => {
  assert.equal(goRFC3339Nano(new Date(Date.UTC(2026, 0, 2, 3, 4, 5))), '2026-01-02T03:04:05Z')
  assert.equal(goRFC3339Nano(new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 120))), '2026-01-02T03:04:05.12Z')
  assert.equal(goRFC3339Nano(new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 7))), '2026-01-02T03:04:05.007Z')
})
