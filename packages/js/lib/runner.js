// The Runner: executes vectors against one endpoint, streaming results as an
// async generator with a concurrency-limited worker pool.

import { manifest } from '@cloud-portable/s3vectors'
import { S3Client } from '@aws-sdk/client-s3'
import { withDefaults, buildClient, Identities } from './config.js'
import { defaultProvisioner } from './provision.js'
import { runVector } from './vector.js'

/** A tiny push/pull channel closed when the producers finish. */
class AsyncQueue {
  constructor () {
    this.values = []
    this.waiters = []
    this.closed = false
  }

  push (v) {
    const w = this.waiters.shift()
    if (w) w({ value: v, done: false })
    else this.values.push(v)
  }

  close () {
    this.closed = true
    for (const w of this.waiters.splice(0)) w({ value: undefined, done: true })
  }

  next () {
    if (this.values.length > 0) return Promise.resolve({ value: this.values.shift(), done: false })
    if (this.closed) return Promise.resolve({ value: undefined, done: true })
    return new Promise((resolve) => this.waiters.push(resolve))
  }
}

export class Runner {
  /** @param {object} config see Config in index.d.ts; throws on invalid config */
  constructor (config) {
    this.cfg = withDefaults(config)
    this.identities = new Identities(this.cfg)
    const mainClient = buildClient(this.cfg, this.cfg.credentials)
    this.identities.clients.set('main', mainClient)
    this.target = { endpoint: this.cfg.endpoint, region: this.cfg.region, client: mainClient }
    this.rt = {
      cfg: this.cfg,
      identities: this.identities,
      target: this.target,
      defaultProvisioner
    }
  }

  /**
   * The vector corpus snapshot this runner executes — stamp it into reports.
   * @returns {string}
   */
  corpusVersion () {
    return manifest.version
  }

  /**
   * Execute the given vectors, yielding one result per vector in completion
   * order (identical to the given order when concurrency is 1). Selection
   * happens before run — see vectors() and applyFilters().
   *
   * Breaking out of the loop, or aborting `signal`, stops the run: in-flight
   * vectors are cancelled (their resource teardown still runs on its own
   * timeout) and not-yet-started vectors never run — a stopped stream is
   * therefore incomplete. The generator does not return until all in-flight
   * work has wound down.
   *
   * @param {object[]} vectors corpus api vectors
   * @param {{signal?: AbortSignal}} [opts]
   * @returns {AsyncGenerator<object, void, void>} VectorResult stream
   */
  async * run (vectors, { signal } = {}) {
    const ac = new AbortController()
    const onOuter = () => ac.abort(signal.reason)
    signal?.addEventListener('abort', onOuter, { once: true })
    if (signal?.aborted) ac.abort(signal.reason)

    const queue = new AsyncQueue()
    let next = 0
    const workers = Array.from(
      { length: Math.min(this.cfg.concurrency, vectors.length) },
      () => (async () => {
        while (true) {
          const i = next++
          if (i >= vectors.length || ac.signal.aborted) return
          const result = await runVector(this.rt, vectors[i], ac.signal)
          if (!ac.signal.aborted) queue.push(result)
        }
      })()
    )
    const done = Promise.allSettled(workers).then(() => queue.close())

    try {
      while (true) {
        const { value, done: closed } = await queue.next()
        if (closed) break
        yield value
      }
    } finally {
      // Early break or external abort: cancel outstanding work and wait for
      // in-flight vectors (and their teardowns) to finish before returning.
      ac.abort()
      queue.close()
      await done
      signal?.removeEventListener('abort', onOuter)
    }
  }
}

/**
 * Build an S3Client configured the way the runner's own clients are —
 * useful for auditing/tooling around a run.
 * @param {object} config
 */
export function auditClient (config) {
  const cfg = withDefaults(config)
  return new S3Client({
    endpoint: cfg.endpoint,
    region: cfg.region,
    forcePathStyle: !cfg.virtualHostStyle,
    credentials: cfg.credentials
  })
}
