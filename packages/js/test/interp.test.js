import test from 'node:test'
import assert from 'node:assert/strict'
import { Scope } from '../lib/interp.js'

const scope = () => new Scope({
  env: { endpoint: 'http://localhost:9000', region: 'us-east-1' },
  res: { b1: { name: 'bucket-x' }, o1: { etag: '"abc"', versionId: 'v1' } },
  cap: { uploadId: 'UP123', etag1: '"e1"' },
  data: (name, field) => {
    if (name === 'big' && field === 'md5') return 'deadbeef'
    throw new Error('no such dataset/field')
  }
})

test('string interpolation', () => {
  const sc = scope()
  for (const [input, want] of [
    ['plain', 'plain'],
    ['${res.b1.name}', 'bucket-x'],
    ['prefix-${res.b1.name}-suffix', 'prefix-bucket-x-suffix'],
    ['${cap.uploadId}', 'UP123'],
    ['${env.endpoint}', 'http://localhost:9000'],
    ['${data.big.md5}', 'deadbeef'],
    ['$${res.b1.name}', '${res.b1.name}'], // escaped
    ['cost: $5', 'cost: $5'], // bare $ is literal
    ['a$$b', 'a$$b'],
    ['${res.b1.name}${cap.etag1}', 'bucket-x"e1"']
  ]) {
    assert.equal(sc.string(input), want, input)
  }
})

test('unresolvable placeholders throw', () => {
  const sc = scope()
  for (const input of [
    '${res.missing.name}', '${res.b1.nope}', '${cap.nope}', '${env.secret}',
    '${data.nope.md5}', '${data.big.nope}', '${bogus.x}', '${res.b1}', '${unclosed'
  ]) {
    assert.throws(() => sc.string(input), undefined, input)
  }
})

test('value interpolation rebuilds without mutating', () => {
  const sc = scope()
  const input = { Bucket: '${res.b1.name}', Key: 'k', PartNumber: 1, Nested: { A: ['${cap.uploadId}', 5, true, null] } }
  const out = sc.value(input)
  assert.equal(out.Bucket, 'bucket-x')
  assert.equal(out.PartNumber, 1)
  assert.deepEqual(out.Nested.A, ['UP123', 5, true, null])
  assert.equal(input.Bucket, '${res.b1.name}', 'input must not be mutated')
})
