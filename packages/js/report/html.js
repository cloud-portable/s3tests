// HTML reporter rendering the shared cross-implementation mustache template
// (report/page.mustache — a synced copy of shared/report/page.mustache).
// The view model and all number formatting follow the shared contract so
// identical results render byte-identical pages in every language, enforced
// by the shared golden test (shared/report/fixture.json + golden.html; the
// Go implementation is canonical).

import { readFileSync } from 'node:fs'
import Mustache from 'mustache'

const template = readFileSync(new URL('./page.mustache', import.meta.url), 'utf8')
Mustache.parse(template)

// Escape exactly the entity set Go's renderer (template.HTMLEscapeString)
// uses — mustache.js's default additionally escapes ` = /, which would break
// byte parity.
const GO_ESCAPE = { '&': '&amp;', "'": '&#39;', '<': '&lt;', '>': '&gt;', '"': '&#34;' }
const escapeHtml = (s) => String(s).replace(/[&'<>"]/g, (c) => GO_ESCAPE[c])

/**
 * Consume the results and write the report page to the stream.
 * @param {NodeJS.WritableStream} w
 * @param {Iterable<object> | AsyncIterable<object>} results VectorResults
 * @param {object} [meta] {corpusVersion, target, properties, generatedAt, omitSkipped}
 */
export async function write (w, results, meta = {}) {
  const view = await viewModel(results, meta)
  const page = Mustache.render(template, view, undefined, { escape: escapeHtml })
  await new Promise((resolve, reject) => w.write(page, (err) => err ? reject(err) : resolve()))
}

async function viewModel (results, meta) {
  const groups = []
  const groupIdx = new Map()
  const totals = newCounts()
  let totalTime = 0

  for await (const res of toAsync(results)) {
    if (meta.omitSkipped && res.outcome === 'skipped') continue
    let g = groupIdx.get(res.group)
    if (!g) {
      g = { name: res.group, counts: newCounts(), vectors: [] }
      groupIdx.set(res.group, g)
      groups.push(g)
    }
    addCounts(g.counts, res)
    addCounts(totals, res)
    totalTime += res.duration
    g.vectors.push(vectorView(res))
  }

  groups.sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)

  let firstFail = ''
  const groupViews = []
  for (const g of groups) {
    g.vectors.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
    if (firstFail === '') {
      const f = g.vectors.find((v) => v.badge === 'fail')
      if (f) firstFail = f.id
    }
    groupViews.push({
      name: g.name,
      pctClass: pctClass(g.counts),
      barWidth: barWidth(g.counts),
      open: g.counts.fail + g.counts.errors > 0,
      counts: countsView(g.counts),
      vectors: g.vectors
    })
  }

  const generated = meta.generatedAt ? formatGenerated(meta.generatedAt) : ''
  const properties = Object.keys(meta.properties ?? {}).sort()
    .map((name) => ({ name, value: meta.properties[name] }))
  return {
    target: meta.target ?? '',
    hasTarget: Boolean(meta.target),
    corpusVersion: meta.corpusVersion ?? '',
    hasCorpusVersion: Boolean(meta.corpusVersion),
    generated,
    hasGenerated: generated !== '',
    totalTime: humanDuration(totalTime),
    properties,
    hasProvenance: Boolean(meta.target) || Boolean(meta.corpusVersion) || generated !== '' || properties.length > 0,
    totals: countsView(totals),
    firstFail,
    groups: groupViews
  }
}

const newCounts = () => ({ pass: 0, fail: 0, blocked: 0, errors: 0, skipped: 0, total: 0 })

function addCounts (c, res) {
  c.total++
  switch (res.outcome) {
    case 'pass': c.pass++; break
    case 'fail': res.runnerError ? c.errors++ : c.fail++; break
    case 'blocked': c.blocked++; break
    case 'skipped': c.skipped++; break
  }
}

const attempted = (c) => c.total - c.skipped

function countsView (c) {
  return {
    pass: c.pass,
    fail: c.fail,
    blocked: c.blocked,
    errors: c.errors,
    skipped: c.skipped,
    total: c.total,
    attempted: attempted(c),
    passPct: passPct(c),
    hasFail: c.fail > 0,
    hasBlocked: c.blocked > 0,
    hasErrors: c.errors > 0,
    hasSkipped: c.skipped > 0,
    failZero: c.fail === 0,
    blockedZero: c.blocked === 0,
    errorsZero: c.errors === 0,
    skippedZero: c.skipped === 0
  }
}

// Shared integer-arithmetic formatting rules (see the Go implementation).
function passPct (c) {
  const a = attempted(c)
  if (a === 0) return '—'
  const p10 = Math.floor((1000 * c.pass + Math.floor(a / 2)) / a)
  return `${Math.floor(p10 / 10)}.${p10 % 10}%`
}

function pctClass (c) {
  const a = attempted(c)
  if (a === 0) return ''
  if (100 * c.pass >= 80 * a) return 'high'
  if (100 * c.pass >= 50 * a) return 'medium'
  return 'low'
}

function barWidth (c) {
  const a = attempted(c)
  if (a === 0) return '0'
  return String(Math.floor((100 * c.pass + Math.floor(a / 2)) / a))
}

// Raw-file location of the corpus vector files; a text-fragment URL on it
// opens the file scrolled to the vector's "id" line.
const DEFINITION_BASE = 'https://raw.githubusercontent.com/cloud-portable/s3vectors/main/vectors/'

// Link to the vector's definition in the corpus repository. Group and id are
// plain concatenated, not percent-encoded: the corpus schema restricts both to
// [a-z0-9-], and avoiding an encoder keeps the Go and JS reporters
// byte-identical (their encoders escape different characters). The fragment
// prefix is the pre-encoded form of `"id": "`.
const definitionURL = (group, id) => `${DEFINITION_BASE}${group}.json#:~:text=%22id%22%3A%20%22${id}%22`

function vectorView (res) {
  let badge = res.outcome
  let reason = ''
  let summary = ''
  let detail = ''
  if (res.outcome === 'fail') {
    ;[summary, detail] = failureDetail(res)
    if (res.runnerError) {
      badge = 'error'
      summary = 'runner error: ' + res.runnerError
      if (detail === res.runnerError) detail = ''
    }
  } else if (res.outcome === 'blocked') {
    reason = 'blocked: ' + res.reason
  } else if (res.outcome === 'skipped') {
    reason = 'skipped: ' + res.reason
  }
  const tags = res.tags ?? []
  const warnings = res.warnings ?? []
  const title = res.title ?? ''
  const source = res.source ?? ''
  return {
    id: res.id,
    badge,
    duration: vectorDuration(res.duration),
    title,
    hasTitle: title !== '',
    tags,
    hasTags: tags.length > 0,
    reason,
    hasReason: reason !== '',
    summary,
    hasSummary: summary !== '',
    detail,
    hasDetail: detail !== '',
    warnings,
    source,
    hasSource: source !== '',
    definitionURL: definitionURL(res.group, res.id),
    hasDesc: title !== '' || tags.length > 0,
    hasOutcome: reason !== '' || summary !== '' || detail !== '' || warnings.length > 0
  }
}

function failureDetail (res) {
  if (!res.steps?.length) return [res.reason ?? '', '']
  const step = res.steps[res.steps.length - 1]
  const summary = `step ${step.index + 1} (${step.name}) failed`
  const lines = []
  if (step.err) lines.push(step.err)
  for (const f of step.failures ?? []) lines.push(`${f.field}: expected ${f.expected}, got ${f.actual}`)
  return [summary, lines.join('\n')]
}

// vectorDuration: seconds with exactly three decimals from whole ms (round
// half away from zero).
function vectorDuration (ns) {
  const ms = Math.round(ns / 1e6)
  return `${Math.floor(ms / 1000)}.${String(ms % 1000).padStart(3, '0')}s`
}

// humanDuration: "42.3s" under a minute; Go's Duration.String() form above,
// rounded to whole seconds ("4m12s", "1h0m0s").
function humanDuration (ns) {
  if (ns < 60e9) {
    const ds = Math.round(ns / 1e8)
    return `${Math.floor(ds / 10)}.${ds % 10}s`
  }
  let secs = Math.round(ns / 1e9)
  const h = Math.floor(secs / 3600)
  secs -= h * 3600
  const m = Math.floor(secs / 60)
  secs -= m * 60
  return h > 0 ? `${h}h${m}m${secs}s` : `${m}m${secs}s`
}

function formatGenerated (d) {
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ` +
    `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())} UTC`
}

async function * toAsync (iterable) {
  yield * iterable
}
