// Minimal in-memory S3 for exercising runner mechanics: bucket
// create/delete, object put/get, empty version/upload listings, no locking.
// Port of the Go runner's fakeS3 test helper.

import http from 'node:http'
import { once } from 'node:events'
import { createHash } from 'node:crypto'

export async function startFakeS3 () {
  const buckets = new Map() // name -> Map(key -> Buffer)
  const server = http.createServer((req, res) => {
    const chunks = []
    req.on('data', (c) => chunks.push(c))
    req.on('end', () => handle(req, Buffer.concat(chunks), res, buckets))
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    buckets,
    close: () => new Promise((r) => server.close(r))
  }
}

function handle (req, body, res, buckets) {
  const u = new URL(req.url, 'http://x')
  const parts = u.pathname.replace(/^\/+/, '').split('/')
  const bucket = decodeURIComponent(parts[0] ?? '')
  const key = decodeURIComponent(parts.slice(1).join('/'))
  const objects = buckets.get(bucket)

  const xmlError = (status, code) => {
    res.writeHead(status, { 'content-type': 'application/xml' })
    res.end(req.method === 'HEAD' ? undefined : `<?xml version="1.0"?><Error><Code>${code}</Code><Message>${code}</Message></Error>`)
  }

  if (key === '' && req.method === 'PUT') { // CreateBucket
    buckets.set(bucket, new Map())
    res.writeHead(200).end()
  } else if (key === '' && req.method === 'DELETE') {
    buckets.delete(bucket)
    res.writeHead(204).end()
  } else if (key === '' && u.searchParams.has('uploads')) {
    res.writeHead(200, { 'content-type': 'application/xml' })
    res.end('<?xml version="1.0"?><ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>')
  } else if (key === '' && u.searchParams.has('versions')) {
    let xml = '<?xml version="1.0"?><ListVersionsResult><IsTruncated>false</IsTruncated>'
    for (const k of objects?.keys() ?? []) {
      xml += `<Version><Key>${k}</Key><VersionId>null</VersionId></Version>`
    }
    xml += '</ListVersionsResult>'
    res.writeHead(200, { 'content-type': 'application/xml' }).end(xml)
  } else if (key === '' && u.searchParams.has('object-lock')) {
    xmlError(404, 'ObjectLockConfigurationNotFoundError')
  } else if (key === '' && u.searchParams.has('delete') && req.method === 'POST') {
    for (const m of body.toString().matchAll(/<Key>([^<]*)<\/Key>/g)) objects?.delete(m[1])
    res.writeHead(200, { 'content-type': 'application/xml' })
    res.end('<?xml version="1.0"?><DeleteResult></DeleteResult>')
  } else if (key !== '' && req.method === 'PUT') {
    if (!objects) return xmlError(404, 'NoSuchBucket')
    objects.set(key, body)
    res.writeHead(200, { ETag: `"${createHash('md5').update(body).digest('hex')}"` }).end()
  } else if (key !== '' && (req.method === 'GET' || req.method === 'HEAD')) {
    const data = objects?.get(key)
    if (data === undefined) return xmlError(404, 'NoSuchKey')
    res.writeHead(200, {
      ETag: `"${createHash('md5').update(data).digest('hex')}"`,
      'content-length': String(data.length),
      'content-type': 'application/octet-stream'
    })
    res.end(req.method === 'GET' ? data : undefined)
  } else {
    xmlError(400, 'NotImplemented')
  }
}
