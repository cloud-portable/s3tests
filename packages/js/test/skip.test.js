import { test } from 'node:test'
import assert from 'node:assert/strict'
import { skipReason, defaultSkip, skip } from '../lib/skip.js'
import { tags, tagsMatching, ids } from '../lib/filter.js'

// Mirror runner.run's skip decision: the default quirk rule is prepended before
// the caller's rules, then a noSkip/noSkipMatching unskip opts vectors back in.
const isSkipped = (v, { skip: extra = [], noSkip = [], noSkipMatching = [] } = {}) => {
  const reason = skipReason([defaultSkip, ...extra], v)
  const unskip = [tags(...noSkip), tagsMatching(...noSkipMatching)]
  return reason !== undefined && !unskip.some((u) => u(v))
}

test('quirk vectors are skipped by default; noSkip/noSkipMatching opt back in', () => {
  const quirk = { id: 'a', tags: ['tier-1', 'quirk:not-aws'] }
  const dir = { id: 'b', tags: ['quirk:directory-bucket'] }
  const plain = { id: 'c', tags: ['tier-1', 'listing'] }

  // default
  assert.ok(isSkipped(quirk), 'quirk skipped by default')
  assert.ok(isSkipped(dir), 'directory-bucket quirk skipped by default')
  assert.ok(!isSkipped(plain), 'non-quirk not skipped')

  // noSkip exact
  assert.ok(!isSkipped(dir, { noSkip: ['quirk:directory-bucket'] }), 'noSkip runs the directory-bucket quirk')
  assert.ok(isSkipped(quirk, { noSkip: ['quirk:directory-bucket'] }), 'noSkip of one tag leaves other quirks skipped')

  // noSkipMatching glob
  assert.ok(!isSkipped(quirk, { noSkipMatching: ['quirk:*'] }), 'noSkipMatching quirk:* runs all quirks')
  assert.ok(!isSkipped(dir, { noSkipMatching: ['quirk:*'] }), 'noSkipMatching quirk:* runs all quirks')

  // an explicit Skip is honored, and noSkip unskips it too (not just quirks)
  const flaky = { id: 'd', tags: ['flaky'] }
  assert.ok(isSkipped(flaky, { skip: [skip('flaky here', tagsMatching('flaky'))] }), 'explicit skip honored')
  assert.ok(!isSkipped(flaky, { skip: [skip('flaky here', tagsMatching('flaky'))], noSkip: ['flaky'] }), 'noSkip unskips an explicit skip')
  assert.ok(isSkipped(plain, { skip: [skip('known bug', ids('c'))] }), 'explicit id skip honored')
})
