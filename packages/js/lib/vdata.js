// Per-vector dataset cache: multi-megabyte $prng streams are generated once
// per vector, however many times their bytes or derived values
// (${data.<name>.<field>}) are referenced. Derived fields are computed
// locally from the cached bytes (the corpus datagen's `derived` regenerates
// the dataset on every call); the semantics mirror the datagen reference and
// are asserted equal in the tests.

import { createHash } from 'node:crypto'
import { generate } from '@cloud-portable/s3vectors/datagen'

export class DataCache {
  /** @param {object|undefined} specs the vector's `data` map */
  constructor (specs) {
    this.specs = specs ?? {}
    this.byteCache = new Map()
    this.derivedCache = new Map()
  }

  /**
   * Dataset bytes, generated on first use. The returned view is shared —
   * callers must not mutate it.
   * @param {string} name
   * @returns {Uint8Array}
   */
  bytes (name) {
    let b = this.byteCache.get(name)
    if (!b) {
      // Resolve $slice through the cache: the corpus generate() would
      // regenerate the parent dataset on every slice call.
      const spec = this.specs[name]
      if (spec?.$slice) {
        const d = spec.$slice
        const parentSpec = this.specs[d.of]
        if (!parentSpec) throw new Error(`slice '${name}' references unknown dataset '${d.of}'`)
        if (parentSpec.$slice) throw new Error(`slice '${name}' references slice '${d.of}' (chained slices are not allowed)`)
        const base = this.bytes(d.of)
        if (d.offset + d.length > base.length) {
          throw new Error(`slice '${name}' [${d.offset}, ${d.offset + d.length}) exceeds '${d.of}' size ${base.length}`)
        }
        b = base.subarray(d.offset, d.offset + d.length)
      } else {
        b = generate(this.specs, name)
      }
      this.byteCache.set(name, b)
    }
    return b
  }

  /**
   * A ${data.<name>.<field>} value, memoized and computed from the cached
   * bytes.
   * @param {string} name
   * @param {string} field
   * @returns {string}
   */
  derived (name, field) {
    const key = name + ' ' + field
    let v = this.derivedCache.get(key)
    if (v === undefined) {
      v = deriveField(this.bytes(name), field)
      this.derivedCache.set(key, v)
    }
    return v
  }
}

function deriveField (bytes, field) {
  switch (field) {
    case 'size': return String(bytes.length)
    case 'md5': return createHash('md5').update(bytes).digest('hex')
    case 'etag': return `"${createHash('md5').update(bytes).digest('hex')}"`
    case 'sha256': return createHash('sha256').update(bytes).digest('hex')
    case 'sha256B64': return createHash('sha256').update(bytes).digest('base64')
    case 'sha1B64': return createHash('sha1').update(bytes).digest('base64')
    case 'crc32B64': return u32ToBase64(crc32(bytes, CRC32_TABLE))
    case 'crc32cB64': return u32ToBase64(crc32(bytes, CRC32C_TABLE))
    case 'crc64nvmeB64': return u64ToBase64(crc64nvme(bytes))
    default: throw new Error(`unknown derived data field: ${field}`)
  }
}

// The checksum implementations mirror the corpus datagen reference
// (scripts/datagen.js) byte-for-byte; the vdata tests assert equality with
// the corpus package's own derived() across every field.

function makeCrc32Table (poly) {
  const table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? (c >>> 1) ^ poly : c >>> 1
    table[n] = c >>> 0
  }
  return table
}

const CRC32_TABLE = makeCrc32Table(0xEDB88320)
const CRC32C_TABLE = makeCrc32Table(0x82F63B78)

function crc32 (buf, table) {
  let c = 0xFFFFFFFF
  for (let i = 0; i < buf.length; i++) c = table[(c ^ buf[i]) & 0xFF] ^ (c >>> 8)
  return (c ^ 0xFFFFFFFF) >>> 0
}

// CRC-64/NVME: reflected poly 0x9A6C9329AC4BC9B5, init/xorout all-ones.
const CRC64_TABLE = (() => {
  const poly = 0x9A6C9329AC4BC9B5n
  const table = new BigUint64Array(256)
  for (let n = 0; n < 256; n++) {
    let c = BigInt(n)
    for (let k = 0; k < 8; k++) c = c & 1n ? (c >> 1n) ^ poly : c >> 1n
    table[n] = c
  }
  return table
})()

function crc64nvme (buf) {
  let c = 0xFFFFFFFFFFFFFFFFn
  for (let i = 0; i < buf.length; i++) {
    c = CRC64_TABLE[Number((c ^ BigInt(buf[i])) & 0xFFn)] ^ (c >> 8n)
  }
  return c ^ 0xFFFFFFFFFFFFFFFFn
}

function u32ToBase64 (v) {
  const b = Buffer.alloc(4)
  b.writeUInt32BE(v)
  return b.toString('base64')
}

function u64ToBase64 (v) {
  const b = Buffer.alloc(8)
  b.writeBigUInt64BE(v)
  return b.toString('base64')
}
