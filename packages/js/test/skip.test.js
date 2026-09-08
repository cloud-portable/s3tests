import { test } from 'node:test'
import assert from 'node:assert/strict'
import { skipReason, defaultSkip, skip } from '../lib/skip.js'
import { tags, tagsMatching, ids } from '../lib/filter.js'

// Mirror runner.run's skip decision: the default quirk rule is prepended before
// the caller's rules, then a noSkip filter opts matching vectors back in.
const isSkipped = (v, { skip: extra = [], noSkip = [] } = {}) => {
  const reason = skipReason([defaultSkip, ...extra], v)
  return reason !== undefined && !noSkip.some((u) => u(v))
}

test('quirk vectors are skipped by default; noSkip filters opt back in', () => {
  const quirk = { id: 'a', tags: ['tier-1', 'quirk:not-aws'] }
  const dir = { id: 'b', tags: ['quirk:directory-bucket'] }
  const plain = { id: 'c', tags: ['tier-1', 'listing'] }

  // default
  assert.ok(isSkipped(quirk), 'quirk skipped by default')
  assert.ok(isSkipped(dir), 'directory-bucket quirk skipped by default')
  assert.ok(!isSkipped(plain), 'non-quirk not skipped')

  // noSkip by exact tag opts one back in, leaving other quirks skipped
  assert.ok(!isSkipped(dir, { noSkip: [tags('quirk:directory-bucket')] }), 'noSkip runs the directory-bucket quirk')
  assert.ok(isSkipped(quirk, { noSkip: [tags('quirk:directory-bucket')] }), 'noSkip of one tag leaves other quirks skipped')

  // noSkip by tag glob opts every quirk back in
  assert.ok(!isSkipped(quirk, { noSkip: [tagsMatching('quirk:*')] }), 'noSkip tagsMatching quirk:* runs all quirks')
  assert.ok(!isSkipped(dir, { noSkip: [tagsMatching('quirk:*')] }), 'noSkip tagsMatching quirk:* runs all quirks')

  // noSkip by id runs one specific quirk vector, the rest stay skipped
  assert.ok(!isSkipped(quirk, { noSkip: [ids('a')] }), 'noSkip ids runs the named quirk vector')
  assert.ok(isSkipped(dir, { noSkip: [ids('a')] }), 'noSkip ids must not un-skip other quirks')

  // an explicit Skip is honored, and a noSkip filter unskips it too (not just quirks)
  const flaky = { id: 'd', tags: ['flaky'] }
  assert.ok(isSkipped(flaky, { skip: [skip('flaky here', tagsMatching('flaky'))] }), 'explicit skip honored')
  assert.ok(!isSkipped(flaky, { skip: [skip('flaky here', tagsMatching('flaky'))], noSkip: [tags('flaky')] }), 'noSkip unskips an explicit skip')
})
