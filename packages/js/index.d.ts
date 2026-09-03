// Type declarations for @cloud-portable/s3tests. The implementation is plain
// ESM; these types are maintained by hand and describe the public API only.

/** A test vector from the @cloud-portable/s3vectors corpus (read-only). */
export interface Vector {
  id: string
  kind: 'api'
  group: string
  title?: string
  description?: string
  tags?: string[]
  source?: string
  prerequisites?: unknown[]
  steps: unknown[]
  data?: Record<string, unknown>
  [key: string]: unknown
}

export interface Credential {
  accessKeyId: string
  secretAccessKey: string
  sessionToken?: string
}

export interface Config {
  /** Endpoint URL of the S3 implementation under test. Required. */
  endpoint: string
  /** Main-identity credentials. Required. */
  credentials: Credential
  /** Signing/bucket region. Default "us-east-1". */
  region?: string
  /** Use virtual-host-style addressing instead of path-style. Default false. */
  virtualHostStyle?: boolean
  /** Vectors executed concurrently. Default 1. */
  concurrency?: number
  /** Prefix for runner-created bucket names. Default "s3tests-". */
  bucketPrefix?: string
  /**
   * Supplies credentials for $credential prerequisites (a second identity).
   * Vectors needing one are blocked when unset.
   */
  provisionCredential?: (spec: unknown, options?: { signal?: AbortSignal }) => Promise<Credential>
  /** Overrides the default bucket/object provisioning and teardown. */
  provisioner?: Provisioner
  /** Skip teardown, leaving provisioned resources behind. Default false. */
  keepResources?: boolean
  /** Per-request timeout in milliseconds. Default 60000. */
  requestTimeoutMs?: number
}

/** The endpoint under test, as passed to Provisioner methods. */
export interface Target {
  endpoint: string
  region: string
  /** The runner's main-identity S3Client (from @aws-sdk/client-s3). */
  client: unknown
}

export interface BucketInfo {
  name: string
  /**
   * Keys known to have been written, deleted explicitly during teardown
   * (listings on some implementations hide "foo/bar" while object "foo"
   * exists).
   */
  knownKeys: string[]
}

export interface ObjectInfo {
  key: string
  etag: string
  versionId: string
}

export interface Provisioner {
  bucket (target: Target, prereq: unknown, name: string, options?: { signal?: AbortSignal }): Promise<BucketInfo>
  object (target: Target, prereq: unknown, bucketName: string, body: Uint8Array | null, options?: { signal?: AbortSignal }): Promise<ObjectInfo>
  /** Best-effort cleanup; returns human-readable warnings, never throws. */
  teardown (target: Target, buckets: BucketInfo[], options?: { signal?: AbortSignal }): Promise<string[]>
}

export type Outcome = 'pass' | 'fail' | 'blocked' | 'skipped'

export interface CheckFailure {
  field: string
  expected: string
  actual: string
}

export interface StepResult {
  /** Operation name, or "HTTP <method> <path>" for raw $http steps. */
  name: string
  presigned: boolean
  identity: string
  status: number
  passed: boolean
  failures: CheckFailure[]
  /** Runner-side problem executing the step ('' when none). */
  err: string
  /** Values captured for later steps, or null. */
  captured: Record<string, unknown> | null
  /** Step wall time in nanoseconds. */
  duration: number
}

export interface VectorResult {
  id: string
  group: string
  title: string
  tags: string[]
  source: string
  outcome: Outcome
  /** Why the vector was blocked or skipped ('' otherwise). */
  reason: string
  /** Runner failure detail when the fail is the runner's, not the target's. */
  runnerError: string
  steps: StepResult[]
  warnings: string[]
  /** Vector wall time (including teardown) in nanoseconds. */
  duration: number
}

/** The built-in Provisioner: CreateBucket/PutObject via the runner's client. */
export const defaultProvisioner: Provisioner

export class Runner {
  /** @throws when config.endpoint or config.credentials is missing */
  constructor (config: Config)
  /** Version of the vector corpus this runner executes. */
  corpusVersion (): string
  /**
   * Executes exactly the given vectors, yielding one result per vector as it
   * completes (concurrency per config). Stops promptly when the signal
   * aborts or the iterator is broken out of / returned early.
   */
  run (vectors: Vector[], options?: { signal?: AbortSignal }): AsyncGenerator<VectorResult, void, void>
}

/** Every api-kind vector in the corpus, in manifest order. */
export function vectors (): Vector[]

export type FilterFunc = (v: Vector) => boolean

/** Returns the vectors matching every filter (all vectors when none given). */
export function applyFilters (vectors: Vector[], ...filters: FilterFunc[]): Vector[]
export function groups (...names: string[]): FilterFunc
export function tags (...tags: string[]): FilterFunc
export function ids (...ids: string[]): FilterFunc
export function excludeGroups (...names: string[]): FilterFunc
export function excludeTags (...tags: string[]): FilterFunc
export function excludeIds (...ids: string[]): FilterFunc
