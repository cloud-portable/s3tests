import test from 'node:test'
import assert from 'node:assert/strict'
import { derived, DERIVED_FIELDS } from '@cloud-portable/s3vectors/datagen'
import { DataCache } from '../lib/vdata.js'

const specs = {
  big: { $prng: { seed: 'test-0001/big', size: 100000 } },
  part1: { $slice: { of: 'big', offset: 0, length: 50000 } },
  aaa: { $pattern: { pattern: 'A', size: 16 } }
}

test('derived values match the datagen reference', () => {
  const cache = new DataCache(specs)
  for (const name of ['big', 'part1', 'aaa']) {
    for (const field of DERIVED_FIELDS) {
      assert.equal(cache.derived(name, field), derived(specs, name, field), `${name}.${field}`)
    }
  }
})

test('bytes are memoized', () => {
  const cache = new DataCache(specs)
  assert.equal(cache.bytes('big'), cache.bytes('big'))
})

test('errors on unknown datasets and fields', () => {
  const cache = new DataCache(specs)
  assert.throws(() => cache.bytes('nope'))
  assert.throws(() => cache.derived('big', 'nope'))
})
