import type { TestContext } from 'node:test'
import type { VectorResult } from '../index.js'

/**
 * Reports results as node:test subtests of t — one t.test() per vector:
 * pass returns (details as a diagnostic), blocked/skipped call t.skip, fail
 * and runner errors throw with the failure detail.
 */
export function run (
  t: TestContext,
  results: Iterable<VectorResult> | AsyncIterable<VectorResult>
): Promise<void>

/** Reports a single result inside an already-running subtest. */
export function report (t: TestContext, res: VectorResult): void
