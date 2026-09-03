import test from 'node:test'
import assert from 'node:assert/strict'
import { get, getString } from '../lib/jsonpath.js'

const doc = {
  UploadId: 'UP1',
  ETag: '"abc"',
  Contents: [{ Key: 'a', Size: 5 }, { Key: 'b' }],
  CopyPartResult: { ETag: '"xyz"' },
  headers: { etag: '"h"' },
  status: 200,
  Deep: [['x']]
}

test('get resolves paths', () => {
  for (const [path, want] of [
    ['UploadId', 'UP1'],
    ['Contents[0].Key', 'a'],
    ['Contents[1].Key', 'b'],
    ['Contents[0].Size', 5],
    ['CopyPartResult.ETag', '"xyz"'],
    ['headers.etag', '"h"'],
    ['status', 200],
    ['Deep[0][0]', 'x']
  ]) {
    assert.equal(get(doc, path), want, path)
  }
})

test('get rejects bad paths', () => {
  for (const path of [
    '', 'Nope', 'Contents[2].Key', 'Contents[0].Nope', 'UploadId.Sub',
    'Contents[x]', 'Contents[', 'Contents]', '.Leading', 'Trailing.', 'Contents[-1]'
  ]) {
    assert.throws(() => get(doc, path), undefined, path)
  }
})

test('getString renders scalars', () => {
  assert.equal(getString(doc, 'Contents[0].Size'), '5')
  assert.equal(getString(doc, 'UploadId'), 'UP1')
  assert.throws(() => getString(doc, 'Contents'))
})
