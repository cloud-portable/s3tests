import test from 'node:test'
import assert from 'node:assert/strict'
import { matchValue, matchHeaders, matchError, matchBody, contentValue, compileRegex } from '../lib/match.js'

const enc = (s) => new TextEncoder().encode(s)

test('scalar equality', () => {
  for (const [expected, actual, ok] of [
    ['a', 'a', true],
    ['a', 'b', false],
    [5, 5, true],
    [5, '5', false], // string is not a number
    [true, true, true],
    [null, null, true],
    [10485760, 10485760, true]
  ]) {
    const ms = matchValue('x', expected, actual, true)
    assert.equal(ms.length === 0, ok, JSON.stringify([expected, actual]))
  }
})

test('subset objects', () => {
  const actual = { Key: 'a', Size: 5, Extra: 'ignored' }
  assert.equal(matchValue('', { Key: 'a', Size: 5 }, actual, true).length, 0)
  const ms = matchValue('', { Key: 'b' }, actual, true)
  assert.equal(ms.length, 1)
  assert.equal(ms[0].path, 'Key')
  assert.equal(matchValue('', { Nope: 'x' }, actual, true).length, 1)
})

test('arrays are exact-length and ordered', () => {
  const actual = [{ Key: 'a' }, { Key: 'b' }]
  assert.equal(matchValue('', [{ Key: 'a' }, { Key: 'b' }], actual, true).length, 0)
  assert.notEqual(matchValue('', [{ Key: 'b' }, { Key: 'a' }], actual, true).length, 0)
  assert.notEqual(matchValue('', [{ Key: 'a' }], actual, true).length, 0)
})

test('assertion operators', () => {
  for (const [name, expected, actual, present, ok] of [
    ['exists yes', { $exists: true }, 'v', true, true],
    ['exists no', { $exists: true }, undefined, false, false],
    ['absent yes', { $absent: true }, undefined, false, true],
    ['absent no', { $absent: true }, 'v', true, false],
    ['eq assertion-looking literal', { $eq: { $exists: true } }, { $exists: true }, true, true],
    ['eq scalar', { $eq: 5 }, 5, true, true],
    ['matches', { $matches: '-2"$' }, '"abc-2"', true, true],
    ['matches no', { $matches: '-2"$' }, '"abc-3"', true, false],
    ['matches number actual', { $matches: '^20' }, 2026, true, true],
    ['length arr', { $length: 2 }, ['a', 'b'], true, true],
    ['length str', { $length: 3 }, 'abc', true, true],
    ['length wrong', { $length: 3 }, ['a'], true, false],
    ['contains', { $contains: { Key: 'b' } }, [{ Key: 'a' }, { Key: 'b', X: 1 }], true, true],
    ['contains no', { $contains: 'z' }, ['a', 'b'], true, false],
    ['combined', { $exists: true, $matches: '^a' }, 'abc', true, true],
    ['ne differs', { $ne: '"abc"' }, '"def"', true, true],
    ['ne equal', { $ne: '"abc"' }, '"abc"', true, false],
    ['ne absent', { $ne: '"abc"' }, undefined, false, false],
    ['ne combined', { $ne: '"s"', $matches: '-' }, '"comp-2"', true, true]
  ]) {
    const ms = matchValue('x', expected, actual, present)
    assert.equal(ms.length === 0, ok, name + ': ' + JSON.stringify(ms))
  }
})

test('portable regexes compile natively; lookaheads are absent from the corpus', () => {
  for (const pat of ['-2"$', '^\\d{4}-\\d{2}-\\d{2}', '^(STANDARD|REDUCED_REDUNDANCY|)$']) {
    compileRegex(pat)
  }
})

test('headers', () => {
  const headers = { 'content-range': 'bytes 0-4/10', 'x-amz-request-id': 'R1' }
  const ms = matchHeaders({
    'content-range': 'bytes 0-4/10',
    'x-amz-request-id': { $exists: true },
    'x-amz-missing': { $absent: true }
  }, headers)
  assert.deepEqual(ms, [])
  assert.equal(matchHeaders({ 'content-range': 'bytes 0-5/10' }, headers).length, 1)
})

test('error matching', () => {
  assert.deepEqual(matchError('NoSuchKey', 'NoSuchKey', 'nope'), [])
  assert.equal(matchError('NoSuchKey', 'NoSuchBucket', '').length, 1)
  const obj = { code: 'InvalidURI', message: "Couldn't parse the specified URI." }
  assert.deepEqual(matchError(obj, 'InvalidURI', "Couldn't parse the specified URI."), [])
  assert.equal(matchError(obj, 'InvalidURI', 'other').length, 1)
})

test('body matching', () => {
  const body = enc('hello')
  assert.deepEqual(matchBody('hello', body, null), [])
  assert.equal(matchBody('world', body, null).length, 1)
  assert.deepEqual(matchBody({ $base64: 'aGVsbG8=' }, body, null), [])
  assert.deepEqual(matchBody({ $data: 'part1' }, body, () => enc('hello')), [])
  assert.deepEqual(matchBody({ $size: 5, $md5: '5d41402abc4b2a76b9719d911017c592' }, body, null), [])
  assert.equal(matchBody({ $size: 6 }, body, null).length, 1)
  assert.equal(matchBody({ $sha256: 'nope' }, body, null).length, 1)
})

test('content descriptors', () => {
  assert.deepEqual(contentValue('abc', null), enc('abc'))
  assert.deepEqual([...contentValue({ $base64: 'AQID' }, null)], [1, 2, 3])
  assert.throws(() => contentValue({ $data: 'x' }, null))
  assert.throws(() => contentValue({ other: 1 }, null))
})
