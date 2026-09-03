import type { VectorResult } from '../index.js'
import type { Meta } from './junit.js'

export type { Meta }

/**
 * Writes the single-file, zero-JavaScript HTML report (byte-identical to the
 * Go reporter's output for the same results and meta).
 * @param w a stream.Writable
 * @param results an array or (async) iterable of results — pass runner.run()
 * directly to report while running
 */
export function write (
  w: NodeJS.WritableStream,
  results: Iterable<VectorResult> | AsyncIterable<VectorResult>,
  meta?: Meta
): Promise<void>
