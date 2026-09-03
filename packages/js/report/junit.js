// JUnit XML reporter, mapping per the corpus reporting guide (identical to
// the Go runner's): one <testcase> per vector (name = id, classname =
// group), one <testsuite> per group; blocked -> <skipped message="blocked:
// ...">, never <failure>; RunnerError fails -> <error>; warnings ->
// <system-out>. Output is deterministic; a testsuite timestamp appears only
// when meta.generatedAt is set.

/**
 * Consume the results and write a JUnit XML document to the stream.
 * @param {NodeJS.WritableStream} w
 * @param {Iterable<object> | AsyncIterable<object>} results VectorResults
 * @param {object} [meta] report meta {corpusVersion, target, properties, generatedAt, omitSkipped}
 */
export async function write (w, results, meta = {}) {
  const suites = []
  const suiteIdx = new Map()
  const totals = { tests: 0, failures: 0, errors: 0, skipped: 0, time: 0 }

  for await (const res of toAsync(results)) {
    if (meta.omitSkipped && res.outcome === 'skipped') continue
    let suite = suiteIdx.get(res.group)
    if (!suite) {
      suite = { name: res.group, tests: 0, failures: 0, errors: 0, skipped: 0, time: 0, cases: [] }
      suiteIdx.set(res.group, suite)
      suites.push(suite)
    }
    suite.cases.push(res)
    suite.tests++
    totals.tests++
    if (res.outcome === 'fail') {
      if (res.runnerError) {
        suite.errors++
        totals.errors++
      } else {
        suite.failures++
        totals.failures++
      }
    } else if (res.outcome === 'blocked' || res.outcome === 'skipped') {
      suite.skipped++
      totals.skipped++
    }
    suite.time += res.duration
    totals.time += res.duration
  }

  const out = []
  out.push('<?xml version="1.0" encoding="UTF-8"?>')
  out.push(`<testsuites name="s3vectors" tests="${totals.tests}" failures="${totals.failures}" errors="${totals.errors}" skipped="${totals.skipped}" time="${seconds(totals.time)}">`)
  for (const suite of suites) {
    const ts = timestamp(meta)
    out.push(`  <testsuite name="${attr(suite.name)}" tests="${suite.tests}" failures="${suite.failures}" errors="${suite.errors}" skipped="${suite.skipped}" time="${seconds(suite.time)}"${ts ? ` timestamp="${ts}"` : ''}>`)
    const props = suiteProperties(meta)
    if (props.length > 0) {
      out.push('    <properties>')
      for (const [name, value] of props) out.push(`      <property name="${attr(name)}" value="${attr(value)}"></property>`)
      out.push('    </properties>')
    }
    for (const res of suite.cases) out.push(...testcase(res))
    out.push('  </testsuite>')
  }
  out.push('</testsuites>')
  await writeAll(w, out.join('\n') + '\n')
}

function testcase (res) {
  const lines = []
  const open = `    <testcase name="${attr(res.id)}" classname="${attr(res.group)}" time="${seconds(res.duration)}">`
  lines.push(open)
  if (res.tags?.length > 0) {
    lines.push('      <properties>')
    lines.push(`        <property name="tags" value="${attr(res.tags.join(','))}"></property>`)
    lines.push('      </properties>')
  }
  if (res.outcome === 'fail') {
    const [msg, body] = failureDetail(res)
    if (res.runnerError) {
      lines.push(`      <error message="${attr('runner error: ' + res.runnerError)}">${text(body)}</error>`)
    } else {
      lines.push(`      <failure message="${attr(msg)}">${text(body)}</failure>`)
    }
  } else if (res.outcome === 'blocked') {
    lines.push(`      <skipped message="${attr('blocked: ' + res.reason)}"></skipped>`)
  } else if (res.outcome === 'skipped') {
    lines.push(`      <skipped message="${attr('skipped: ' + res.reason)}"></skipped>`)
  }
  if (res.warnings?.length > 0) {
    lines.push(`      <system-out>${text(res.warnings.join('\n'))}</system-out>`)
  }
  lines.push('    </testcase>')
  return lines
}

// failureDetail summarizes the failing (last executed) step: a one-line
// message (step reference + first mismatch) plus the full detail as text.
function failureDetail (res) {
  if (!res.steps?.length) return [res.reason ?? '', '']
  const step = res.steps[res.steps.length - 1]
  const prefix = `step ${step.index + 1} (${step.name})`
  const lines = []
  if (step.err) lines.push(step.err)
  for (const f of step.failures ?? []) lines.push(`${f.field}: expected ${f.expected}, got ${f.actual}`)
  if (lines.length === 0) return [prefix + ': failed', '']
  return [prefix + ': ' + lines[0], lines.join('\n')]
}

function suiteProperties (meta) {
  const props = []
  if (meta.corpusVersion) props.push(['corpusVersion', meta.corpusVersion])
  if (meta.target) props.push(['target', meta.target])
  for (const k of Object.keys(meta.properties ?? {}).sort()) props.push([k, meta.properties[k]])
  return props
}

function timestamp (meta) {
  if (!meta.generatedAt) return ''
  const d = meta.generatedAt
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}` +
    `T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`
}

// seconds renders nanoseconds as seconds with exactly three decimals (round
// half away from zero, matching the shared cross-implementation rule).
function seconds (ns) {
  const ms = Math.round(ns / 1e6)
  return `${Math.floor(ms / 1000)}.${String(ms % 1000).padStart(3, '0')}`
}

const attr = (s) => String(s)
  .replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
  .replaceAll('"', '&quot;').replaceAll("'", '&apos;')

const text = (s) => String(s)
  .replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')

async function * toAsync (iterable) {
  yield * iterable
}

function writeAll (w, data) {
  return new Promise((resolve, reject) => {
    w.write(data, (err) => err ? reject(err) : resolve())
  })
}
