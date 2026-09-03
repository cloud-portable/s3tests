import type { VectorResult } from '../index.js'

export interface Meta {
  corpusVersion?: string
  target?: string
  properties?: Record<string, string>
  generatedAt?: Date
  /** Leave skipped vectors out of the report entirely. */
  omitSkipped?: boolean
}

/**
 * Writes a JUnit XML report: one testsuite per group, blocked/skipped map to
 * <skipped>, runner errors to <error>, check failures to <failure>.
 * @param w a stream.Writable (backpressure respected)
 * @param results an array or (async) iterable of results — pass runner.run()
 * directly to report while running
 */
export function write (
  w: NodeJS.WritableStream,
  results: Iterable<VectorResult> | AsyncIterable<VectorResult>,
  meta?: Meta
): Promise<void>
