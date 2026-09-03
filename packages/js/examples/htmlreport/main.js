// Runs the compatibility corpus against an S3 endpoint and writes the HTML
// report — the example consumer of the library API: select vectors with
// filters, stream run() results straight into a reporter.
//
//   node examples/htmlreport/main.js --target "MinIO (local docker)" -o report.html
//   node examples/htmlreport/main.js --tags tier-1 -o report-tier-1.html

import { parseArgs } from 'node:util'
import { createWriteStream } from 'node:fs'
import { once } from 'node:events'
import { Runner, vectors, applyFilters, groups, tags } from '../../index.js'
import * as html from '../../report/html.js'

const { values } = parseArgs({
  options: {
    endpoint: { type: 'string', default: 'http://127.0.0.1:9000' },
    'access-key': { type: 'string', default: 'minioadmin' },
    'secret-key': { type: 'string', default: 'minioadmin' },
    target: { type: 'string' },
    groups: { type: 'string' },
    tags: { type: 'string' },
    concurrency: { type: 'string', default: '4' },
    o: { type: 'string', default: 'report.html' }
  }
})

const runner = new Runner({
  endpoint: values.endpoint,
  credentials: { accessKeyId: values['access-key'], secretAccessKey: values['secret-key'] },
  concurrency: Number(values.concurrency)
})

const filters = []
const properties = {}
if (values.groups) {
  filters.push(groups(...values.groups.split(',')))
  properties.groups = values.groups
}
if (values.tags) {
  filters.push(tags(...values.tags.split(',')))
  properties.tags = values.tags
}
const selected = applyFilters(vectors(), ...filters)
if (selected.length === 0) throw new Error('no vectors selected')

const f = createWriteStream(values.o)
await html.write(f, runner.run(selected), {
  corpusVersion: runner.corpusVersion(),
  target: values.target ?? values.endpoint,
  properties,
  generatedAt: new Date()
})
f.end()
await once(f, 'close')
console.log(`wrote ${values.o} (${selected.length} vectors, corpus ${runner.corpusVersion()})`)
