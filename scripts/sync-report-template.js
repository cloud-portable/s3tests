#!/usr/bin/env node
// Synchronize the canonical HTML report template into the language packages.
//
//   node scripts/sync-report-template.js           write mode
//   node scripts/sync-report-template.js --check   verify copies match; exit 1 on drift
//
// shared/report/page.mustache is the single source; the copies embedded by
// each runner are never edited directly.
'use strict'

const fs = require('node:fs')
const path = require('node:path')

const ROOT = path.join(__dirname, '..')
const CHECK = process.argv.includes('--check')

const SOURCE = path.join(ROOT, 'shared', 'report', 'page.mustache')
const DESTS = [
  'packages/go/report/html/page.mustache',
  'packages/js/report/page.mustache',
  'packages/python/src/cloud_portable_s3tests/report/page.mustache'
]

const canonical = fs.readFileSync(SOURCE)
let drift = 0
for (const dest of DESTS) {
  const abs = path.join(ROOT, dest)
  const same = fs.existsSync(abs) && fs.readFileSync(abs).equals(canonical)
  if (!same) {
    drift++
    console.error(`${CHECK ? 'DRIFT' : 'FIX'} ${dest}`)
    if (!CHECK) {
      fs.mkdirSync(path.dirname(abs), { recursive: true })
      fs.writeFileSync(abs, canonical)
    }
  }
}

if (CHECK && drift > 0) {
  console.error(`\n${drift} template copy(ies) out of sync — run: node scripts/sync-report-template.js`)
  process.exit(1)
}
console.log(CHECK ? 'report template sync check OK' : `report template synced to ${DESTS.length} package(s) (${drift} updated)`)
