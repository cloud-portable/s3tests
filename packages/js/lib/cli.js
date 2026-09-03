// The s3tests CLI: run the corpus against an endpoint, stream console
// progress, and write file reports for each --report/-r flag.
//
// By default results just stream to the console. Reports are written for
// each --report (-r) flag, given as <format> (default path: report.xml for
// junit, report.html for html) or <format>=<path>, repeatable. The exit code
// is 1 when any vector failed (including runner errors) and 0 otherwise;
// blocked vectors do not affect it (a missing second identity blocks the
// $credential vectors by design — supply --alt-access-key/--alt-secret-key
// to run them).

import { parseArgs } from 'node:util'
import { createWriteStream } from 'node:fs'
import { once } from 'node:events'
import { Runner, vectors, applyFilters, groups, tags, ids, excludeGroups, excludeTags, excludeIds } from '../index.js'
import * as junit from '../report/junit.js'
import * as html from '../report/html.js'

const REPORTERS = {
  junit: { write: junit.write, defaultPath: 'report.xml' },
  html: { write: html.write, defaultPath: 'report.html' }
}

const OPTIONS = {
  endpoint: { type: 'string' },
  'access-key': { type: 'string' },
  'secret-key': { type: 'string' },
  region: { type: 'string', default: 'us-east-1' },
  'virtual-host': { type: 'boolean', default: false },
  concurrency: { type: 'string', default: '1' },
  'keep-resources': { type: 'boolean', default: false },
  'alt-access-key': { type: 'string' },
  'alt-secret-key': { type: 'string' },
  'alt-canonical-id': { type: 'string' },
  'alt-display-name': { type: 'string' },
  groups: { type: 'string' },
  tags: { type: 'string' },
  ids: { type: 'string' },
  'exclude-groups': { type: 'string' },
  'exclude-tags': { type: 'string' },
  'exclude-ids': { type: 'string' },
  report: { type: 'string', multiple: true, short: 'r' },
  target: { type: 'string' },
  quiet: { type: 'boolean', default: false },
  help: { type: 'boolean', short: 'h', default: false }
}

const USAGE = `usage: s3tests --endpoint <url> --access-key <id> --secret-key <key> [options]

connection:
  --endpoint <url>          S3 endpoint under test (env S3TESTS_ENDPOINT)
  --access-key <id>         access key id (env S3TESTS_ACCESS_KEY)
  --secret-key <key>        secret access key (env S3TESTS_SECRET_KEY)
  --region <region>         SigV4 region (default us-east-1)
  --virtual-host            virtual-hosted-style addressing (default path-style)
  --concurrency <n>         vectors executed in parallel (default 1)
  --keep-resources          skip teardown, leaving buckets for debugging
  --alt-access-key <id>     second identity for $credential vectors (env S3TESTS_ALT_ACCESS_KEY)
  --alt-secret-key <key>    second identity secret key (env S3TESTS_ALT_SECRET_KEY)
  --alt-canonical-id <id>   second identity canonical id (for ACL vectors)
  --alt-display-name <name> second identity display name

selection (comma-separated):
  --groups, --tags, --ids and --exclude-groups, --exclude-tags, --exclude-ids

reporting:
  -r, --report <format>[=<path>]  write a report (formats: ${Object.keys(REPORTERS).sort().join(', ')};
                                  default paths report.xml, report.html); repeatable
  --target <name>                 target name stamped into reports (defaults to the endpoint)
  --quiet                         suppress per-vector progress output`

/**
 * Run the CLI; returns the process exit code.
 * @param {string[]} argv
 * @param {NodeJS.WritableStream} stdout
 * @param {NodeJS.WritableStream} stderr
 */
export async function run (argv, stdout, stderr) {
  let values
  try {
    values = parseArgs({ args: argv, options: OPTIONS, allowPositionals: false }).values
  } catch (err) {
    stderr.write(`error: ${err.message}\n${USAGE}\n`)
    return 2
  }
  if (values.help) {
    stdout.write(USAGE + '\n')
    return 0
  }

  const env = (name) => process.env[name] ?? ''
  const endpoint = values.endpoint ?? env('S3TESTS_ENDPOINT')
  const accessKey = values['access-key'] ?? env('S3TESTS_ACCESS_KEY')
  const secretKey = values['secret-key'] ?? env('S3TESTS_SECRET_KEY')
  if (!endpoint || !accessKey || !secretKey) {
    stderr.write(`error: --endpoint, --access-key and --secret-key are required\n${USAGE}\n`)
    return 2
  }

  let reports
  try {
    reports = (values.report ?? []).map(parseReportSpec)
  } catch (err) {
    stderr.write(`error: ${err.message}\n`)
    return 2
  }

  const config = {
    endpoint,
    region: values.region,
    credentials: { accessKeyId: accessKey, secretAccessKey: secretKey },
    virtualHostStyle: values['virtual-host'],
    concurrency: Number(values.concurrency) || 1,
    keepResources: values['keep-resources']
  }
  const altAccess = values['alt-access-key'] ?? env('S3TESTS_ALT_ACCESS_KEY')
  const altSecret = values['alt-secret-key'] ?? env('S3TESTS_ALT_SECRET_KEY')
  if (altAccess && altSecret) {
    const cred = {
      accessKeyId: altAccess,
      secretAccessKey: altSecret,
      canonicalId: values['alt-canonical-id'] ?? env('S3TESTS_ALT_CANONICAL_ID'),
      displayName: values['alt-display-name'] ?? env('S3TESTS_ALT_DISPLAY_NAME')
    }
    config.provisionCredential = async () => cred
  }

  let runner
  try {
    runner = new Runner(config)
  } catch (err) {
    stderr.write(`error: ${err.message}\n`)
    return 2
  }

  const { filters, properties } = buildFilters(values)
  const selected = applyFilters(vectors(), ...filters)
  if (selected.length === 0) {
    stderr.write('error: no vectors selected\n')
    return 2
  }

  // Ctrl-C cancels the run; in-flight vectors still tear their buckets down.
  // A second interrupt hard-exits.
  const ac = new AbortController()
  let interrupted = false
  const onSigint = () => {
    if (interrupted) process.exit(130)
    interrupted = true
    stderr.write('\ninterrupted — cancelling (Ctrl-C again to force quit)\n')
    ac.abort()
  }
  process.on('SIGINT', onSigint)

  const color = colorsEnabled(stdout)
  const counts = { pass: 0, fail: 0, blocked: 0, skipped: 0 }
  let runnerErrs = 0
  const results = []
  const started = Date.now()
  try {
    for await (const res of runner.run(selected, { signal: ac.signal })) {
      results.push(res)
      counts[res.outcome]++
      if (res.runnerError) runnerErrs++
      if (!values.quiet) stdout.write(progressLine(res, color) + '\n')
    }
  } finally {
    process.off('SIGINT', onSigint)
  }
  const wallSecs = (Date.now() - started) / 1000

  const meta = {
    corpusVersion: runner.corpusVersion(),
    target: values.target ?? endpoint,
    properties,
    generatedAt: new Date()
  }
  let reporterFailed = false
  for (const spec of reports) {
    try {
      const f = createWriteStream(spec.path)
      await REPORTERS[spec.format].write(f, results, meta)
      f.end()
      await once(f, 'close')
      stdout.write(`wrote ${spec.format} report ${spec.path}\n`)
    } catch (err) {
      stderr.write(`error: writing ${spec.format} report: ${err.message}\n`)
      reporterFailed = true
    }
  }

  const attempted = results.length
  const pct = attempted > 0 ? `${(100 * counts.pass / attempted).toFixed(1)}%` : '—'
  stdout.write(`\n${attempted} vectors: ${counts.pass} pass, ${counts.fail} fail (${runnerErrs} runner errors), ${counts.blocked} blocked — ${pct} pass rate in ${wallSecs.toFixed(1)}s (corpus ${runner.corpusVersion()})\n`)

  if (counts.fail > 0 || reporterFailed) return 1
  if (interrupted) return 130
  return 0
}

function parseReportSpec (value) {
  const eq = value.indexOf('=')
  const format = eq < 0 ? value : value.slice(0, eq)
  const path = eq < 0 ? '' : value.slice(eq + 1)
  if (format === '' || (eq >= 0 && path === '')) {
    throw new Error(`expected <format> or <format>=<path>, got ${JSON.stringify(value)}`)
  }
  const reporter = REPORTERS[format]
  if (!reporter) {
    throw new Error(`unknown report format ${JSON.stringify(format)} (formats: ${Object.keys(REPORTERS).sort().join(', ')})`)
  }
  return { format, path: path || reporter.defaultPath }
}

// buildFilters turns the selection flags into filter funcs, plus the
// properties stamped into reports so filtered runs self-describe.
function buildFilters (values) {
  const filters = []
  const properties = {}
  const add = (name, ctor) => {
    const val = values[name]
    if (!val) return
    filters.push(ctor(...val.split(',')))
    properties[name] = val
  }
  add('groups', groups)
  add('tags', tags)
  add('ids', ids)
  add('exclude-groups', excludeGroups)
  add('exclude-tags', excludeTags)
  add('exclude-ids', excludeIds)
  return { filters, properties }
}

const ANSI = { reset: '\x1b[0m', green: '\x1b[32m', red: '\x1b[31m', amber: '\x1b[33m', violet: '\x1b[35m' }

function colorsEnabled (stdout) {
  return !process.env.NO_COLOR && stdout.isTTY === true
}

function progressLine (res, color) {
  let outcome = res.outcome
  let tint = ''
  let detail = ''
  if (res.outcome === 'pass') {
    tint = ANSI.green
  } else if (res.outcome === 'fail') {
    tint = ANSI.red
    if (res.runnerError) {
      outcome = 'error'
      tint = ANSI.violet
      detail = ` — runner error: ${res.runnerError}`
    } else if (res.steps?.length > 0) {
      const step = res.steps[res.steps.length - 1]
      detail = ` — step ${step.index + 1} (${step.name}) failed`
      if (step.failures?.length > 0) {
        const f = step.failures[0]
        detail += `: ${f.field}: expected ${f.expected}, got ${f.actual}`
      } else if (step.err) {
        detail += `: ${step.err}`
      }
    }
  } else if (res.outcome === 'blocked') {
    tint = ANSI.amber
    detail = ` — ${res.reason}`
  }
  if (color && tint) outcome = tint + outcome + ANSI.reset
  return `${outcome.padStart(color && tint ? 8 + tint.length + ANSI.reset.length : 8)} ${res.id} (${(res.duration / 1e9).toFixed(2)}s)${detail}`
}
