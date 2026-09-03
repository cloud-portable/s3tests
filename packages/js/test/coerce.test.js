import test from 'node:test'
import assert from 'node:assert/strict'
import { buildInput, parseTime } from '../lib/coerce.js'

test('timestamps convert to Dates in all corpus formats', () => {
  const { input } = buildInput({
    CopySourceIfModifiedSince: '2026-01-02T03:04:05Z',
    Expires: 'Sun, 01 Jan 2034 00:00:00 GMT',
    LifecycleConfiguration: {
      Rules: [{
        Expiration: { Date: '2023-09-27' },
        Transitions: [{ Date: '20220927', StorageClass: 'GLACIER' }]
      }]
    },
    Delete: { Objects: [{ Key: 'k', LastModifiedTime: '2026-01-01T00:00:00Z' }] }
  }, null)
  assert.equal(input.CopySourceIfModifiedSince.toISOString(), '2026-01-02T03:04:05.000Z')
  assert.equal(input.Expires.getUTCFullYear(), 2034)
  assert.equal(input.LifecycleConfiguration.Rules[0].Expiration.Date.toISOString(), '2023-09-27T00:00:00.000Z')
  assert.equal(input.LifecycleConfiguration.Rules[0].Transitions[0].Date.toISOString(), '2022-09-27T00:00:00.000Z')
  assert.ok(input.Delete.Objects[0].LastModifiedTime instanceof Date)
  assert.throws(() => parseTime('not a time'))
  assert.throws(() => buildInput({ Expires: 'garbage' }, null))
})

test('Body content descriptors resolve to bytes and are held aside', () => {
  const resolve = () => new TextEncoder().encode('DATA')
  const { input, body } = buildInput({ Bucket: 'b', Body: { $data: 'part1' } }, resolve)
  assert.deepEqual(body, new TextEncoder().encode('DATA'))
  assert.deepEqual(input.Body, body)
  const plain = buildInput({ Body: 'hello' }, null)
  assert.deepEqual(plain.body, new TextEncoder().encode('hello'))
})

test('CopySource object form composes the string', () => {
  assert.equal(buildInput({ CopySource: { Bucket: 'b', Key: 'src' } }, null).input.CopySource, 'b/src')
  assert.equal(
    buildInput({ CopySource: { Bucket: 'b', Key: 'src', VersionId: 'v1' } }, null).input.CopySource,
    'b/src?versionId=v1')
  assert.equal(buildInput({ CopySource: 'b/plain' }, null).input.CopySource, 'b/plain')
})

test('SSE-C keys stay base64 strings', () => {
  const key = 'pO3upElrwuEXSoFwCfnZPdSsmt/xWeFa0N9KgDijwVs='
  assert.equal(buildInput({ SSECustomerKey: { $base64: key } }, null).input.SSECustomerKey, key)
  assert.equal(buildInput({ SSECustomerKey: key }, null).input.SSECustomerKey, key)
})

test('PartNumberMarker numbers become strings', () => {
  assert.equal(buildInput({ PartNumberMarker: 3 }, null).input.PartNumberMarker, '3')
  assert.equal(buildInput({ MaxParts: 1 }, null).input.MaxParts, 1)
})

test('unions pass through as plain objects', () => {
  const { input } = buildInput({ MetricsConfiguration: { Id: 'x', Filter: { Prefix: 'documents/' } } }, null)
  assert.deepEqual(input.MetricsConfiguration.Filter, { Prefix: 'documents/' })
})
