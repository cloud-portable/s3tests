// Config defaults and the per-identity client registry.

import { S3Client } from '@aws-sdk/client-s3'
import { NodeHttpHandler } from '@smithy/node-http-handler'

export const IDENTITY_MAIN = 'main'
export const IDENTITY_ANONYMOUS = 'anonymous'
export const IDENTITY_INVALID = 'invalid'

// Well-formed credentials under an access key the server cannot know (same
// constants as the Go runner).
export const INVALID_CREDENTIALS = {
  accessKeyId: 'AKIAS3TESTSINVALID00',
  secretAccessKey: 's3tests-invalid-secret-key-0000000000000'
}

/** @param {object} config user config */
export function withDefaults (config) {
  if (!config || typeof config.endpoint !== 'string' || config.endpoint === '') {
    throw new Error('s3tests: config.endpoint is required')
  }
  if (!config.credentials || typeof config.credentials.accessKeyId !== 'string') {
    throw new Error('s3tests: config.credentials is required')
  }
  return {
    endpoint: config.endpoint,
    region: config.region ?? 'us-east-1',
    credentials: config.credentials,
    virtualHostStyle: config.virtualHostStyle ?? false,
    concurrency: Math.max(1, config.concurrency ?? 1),
    bucketPrefix: config.bucketPrefix ?? 's3tests-',
    provisionCredential: config.provisionCredential ?? null,
    provisioner: config.provisioner ?? null,
    keepResources: config.keepResources ?? false,
    requestTimeoutMs: config.requestTimeoutMs ?? 60_000
  }
}

/**
 * Build an S3 client tuned for compatibility testing: the vectors own their
 * wire bytes (no implicit checksums, no retries, no Expect: 100-continue)
 * and expectations need the first response, verbatim.
 * @param {ReturnType<typeof withDefaults>} cfg
 * @param {object | 'anonymous'} credentials
 */
export function buildClient (cfg, credentials) {
  const client = new S3Client({
    endpoint: cfg.endpoint,
    region: cfg.region,
    forcePathStyle: !cfg.virtualHostStyle,
    credentials: credentials === 'anonymous'
      ? { accessKeyId: '', secretAccessKey: '' }
      : credentials,
    maxAttempts: 1,
    requestChecksumCalculation: 'WHEN_REQUIRED',
    responseChecksumValidation: 'WHEN_REQUIRED',
    requestHandler: new NodeHttpHandler({
      requestTimeout: cfg.requestTimeoutMs,
      connectionTimeout: cfg.requestTimeoutMs
    })
  })
  // Expect: 100-continue OFF (the Go runner's ContinueHeaderThresholdBytes: -1).
  client.middlewareStack.remove('addExpectContinueMiddleware')
  if (credentials === 'anonymous') {
    // Unsigned requests: replace the signer with a pass-through.
    client.middlewareStack.addRelativeTo((next) => async (args) => {
      if (args.request?.headers) {
        delete args.request.headers.authorization
        delete args.request.headers.Authorization
        delete args.request.headers['x-amz-date']
        delete args.request.headers['x-amz-content-sha256']
        delete args.request.headers['x-amz-security-token']
      }
      return next(args)
    }, { name: 's3testsAnonymous', relation: 'after', toMiddleware: 'awsAuthMiddleware' })
  }
  return client
}

/** Per-run identity registry: lazily built clients + raw credentials. */
export class Identities {
  /** @param {ReturnType<typeof withDefaults>} cfg */
  constructor (cfg) {
    this.cfg = cfg
    this.clients = new Map()
    this.credentials = new Map([[IDENTITY_MAIN, cfg.credentials]])
    this.altPromises = new Map()
  }

  /** Raw credentials for an identity (used by $http signing). */
  async resolveCredentials (identity) {
    if (this.credentials.has(identity)) return this.credentials.get(identity)
    if (identity === IDENTITY_INVALID) return INVALID_CREDENTIALS
    if (identity === IDENTITY_ANONYMOUS) return null
    const cred = await this.provisionAlt(identity)
    return {
      accessKeyId: cred.accessKeyId,
      secretAccessKey: cred.secretAccessKey,
      sessionToken: cred.sessionToken
    }
  }

  /** Provisioned $credential identity, once per run per handle. */
  provisionAlt (handle) {
    if (!this.cfg.provisionCredential) {
      return Promise.reject(new Error(`no provisionCredential configured (required for $credential prerequisite ${JSON.stringify(handle)})`))
    }
    let p = this.altPromises.get(handle)
    if (!p) {
      p = Promise.resolve(this.cfg.provisionCredential(handle))
      // Cache rejections too (one provisioning attempt per run), but mark
      // them handled so an early failure can't crash the process.
      p.catch(() => {})
      this.altPromises.set(handle, p)
    }
    return p
  }

  /** The (cached) S3 client for an identity. */
  async client (identity) {
    let c = this.clients.get(identity)
    if (!c) {
      const creds = identity === IDENTITY_ANONYMOUS ? 'anonymous' : await this.resolveCredentials(identity)
      c = buildClient(this.cfg, creds)
      this.clients.set(identity, c)
    }
    return c
  }
}
