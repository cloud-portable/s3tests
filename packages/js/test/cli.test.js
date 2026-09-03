import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { readFileSync, existsSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { Writable } from 'node:stream'
import { run } from '../lib/cli.js'
import { vectors } from '../index.js'

// 500-everything stub: vectors with prerequisites block, vectors without
// fail their first step.
async function fail500 () {
  const server = http.createServer((req, res) => {
    req.resume()
    req.on('end', () => {
      res.writeHead(500, { 'content-type': 'application/xml' })
      res.end('<?xml version="1.0"?><Error><Code>Boom</Code><Message>nope</Message></Error>')
    })
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    close: () => new Promise((r) => server.close(r))
  }
}

const sink = () => {
  const chunks = []
  const stream = new Writable({
    write (chunk, enc, cb) {
      chunks.push(String(chunk))
      cb()
    }
  })
  stream.text = () => chunks.join('')
  return stream
}

const pickVector = (want) => {
  const v = vectors().find(want)
  assert.ok(v, 'no matching vector in corpus')
  return v.id
}

test('failures exit 1 and write every requested report', async () => {
  const srv = await fail500()
  try {
    const id = pickVector((v) => (v.prerequisites ?? []).length === 0)
    const dir = mkdtempSync(path.join(tmpdir(), 's3tests-cli-'))
    const junitPath = path.join(dir, 'report.xml')
    const htmlPath = path.join(dir, 'report.html')
    const out = sink()
    const errOut = sink()
    const code = await run([
      '--endpoint', srv.url, '--access-key', 'AK', '--secret-key', 'SK',
      '--ids', id, '-r', `junit=${junitPath}`, '--report', `html=${htmlPath}`
    ], out, errOut)
    assert.equal(code, 1, out.text() + errOut.text())
    for (const want of [id, '1 fail', 'wrote junit report', 'wrote html report']) {
      assert.ok(out.text().includes(want), `missing ${JSON.stringify(want)}:\n${out.text()}`)
    }
    for (const p of [junitPath, htmlPath]) {
      assert.ok(readFileSync(p, 'utf8').includes(id), `${p} must mention ${id}`)
    }
  } finally {
    await srv.close()
  }
})

test('blocked-only runs exit 0; --quiet suppresses progress', async () => {
  const srv = await fail500()
  try {
    const id = pickVector((v) => v.prerequisites?.[0]?.$bucket)
    const out = sink()
    const code = await run([
      '--endpoint', srv.url, '--access-key', 'AK', '--secret-key', 'SK',
      '--ids', id, '--quiet'
    ], out, sink())
    assert.equal(code, 0, out.text())
    assert.ok(out.text().includes('1 blocked'))
    assert.ok(!out.text().includes('blocked ' + id), '--quiet must suppress progress lines')
  } finally {
    await srv.close()
  }
})

test('usage errors exit 2', async () => {
  const errOut = sink()
  assert.equal(await run(['--access-key', 'AK'], sink(), errOut), 2)
  assert.ok(errOut.text().includes('--endpoint'))
  assert.equal(await run([
    '--endpoint', 'http://x', '--access-key', 'AK', '--secret-key', 'SK', '--ids', 'no-such-0001'
  ], sink(), sink()), 2)
})

test('--report validation', async () => {
  const base = ['--endpoint', 'http://x', '--access-key', 'AK', '--secret-key', 'SK']
  for (const bad of ['tap=report.tap', 'tap', 'junit=', '=x']) {
    assert.equal(await run([...base, '-r', bad], sink(), sink()), 2, bad)
  }
  const errOut = sink()
  await run([...base, '-r', 'tap=x'], sink(), errOut)
  assert.ok(errOut.text().includes('formats: html, junit'))
})

test('bare format names use default paths in the working directory', async () => {
  const srv = await fail500()
  const prevCwd = process.cwd()
  try {
    const id = pickVector((v) => (v.prerequisites ?? []).length === 0)
    const dir = mkdtempSync(path.join(tmpdir(), 's3tests-cli-'))
    process.chdir(dir)
    const out = sink()
    await run([
      '--endpoint', srv.url, '--access-key', 'AK', '--secret-key', 'SK',
      '--ids', id, '--quiet', '-r', 'junit', '-r', 'html'
    ], out, sink())
    for (const p of ['report.xml', 'report.html']) {
      assert.ok(existsSync(path.join(dir, p)), `${p} missing`)
      assert.ok(out.text().includes('report ' + p))
    }
  } finally {
    process.chdir(prevCwd)
    await srv.close()
  }
})
