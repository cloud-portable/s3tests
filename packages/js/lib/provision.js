// Prerequisite provisioning and best-effort teardown against the endpoint
// under test itself (the default provisioner), using the main identity.

import {
  CreateBucketCommand, PutBucketVersioningCommand, PutObjectCommand,
  ListMultipartUploadsCommand, AbortMultipartUploadCommand,
  GetObjectLockConfigurationCommand, ListObjectVersionsCommand,
  PutObjectLegalHoldCommand, DeleteObjectsCommand, DeleteObjectCommand,
  DeleteBucketCommand
} from '@aws-sdk/client-s3'

/** The built-in Provisioner: CreateBucket/PutObject via the runner's client. */
export const defaultProvisioner = {
  async bucket (target, prereq, name, { signal } = {}) {
    const input = { Bucket: name }
    if (prereq.objectLock === true) input.ObjectLockEnabledForBucket = true
    if (target.region !== 'us-east-1') {
      input.CreateBucketConfiguration = { LocationConstraint: target.region }
    }
    try {
      await target.client.send(new CreateBucketCommand(input), { abortSignal: signal })
    } catch (err) {
      throw new Error(`CreateBucket ${name}: ${err.message}`)
    }
    if (prereq.versioning) {
      try {
        await target.client.send(new PutBucketVersioningCommand({
          Bucket: name,
          VersioningConfiguration: { Status: prereq.versioning }
        }), { abortSignal: signal })
      } catch (err) {
        throw new Error(`PutBucketVersioning ${name}=${prereq.versioning}: ${err.message}`)
      }
    }
    return { name, knownKeys: [] }
  },

  async object (target, prereq, bucketName, body, { signal } = {}) {
    const input = {
      Bucket: bucketName,
      Key: prereq.key,
      Body: body ?? new Uint8Array(0)
    }
    if (prereq.contentType) input.ContentType = prereq.contentType
    if (prereq.metadata && Object.keys(prereq.metadata).length > 0) input.Metadata = prereq.metadata
    let out
    try {
      out = await target.client.send(new PutObjectCommand(input), { abortSignal: signal })
    } catch (err) {
      throw new Error(`PutObject ${bucketName}/${prereq.key}: ${err.message}`)
    }
    return { key: prereq.key, etag: out.ETag ?? '', versionId: out.VersionId ?? '' }
  },

  async teardown (target, buckets, { signal } = {}) {
    const warnings = []
    for (const b of buckets) {
      warnings.push(...await teardownBucket(target.client, b.name, b.knownKeys ?? [], signal))
    }
    return warnings
  }
}

const isNoSuchBucket = (err) => err?.name === 'NoSuchBucket' || err?.name === 'NotFound'

// teardownBucket empties and deletes one bucket, best-effort: abort multipart
// uploads, delete known-written keys explicitly (some servers' listings hide
// keys), delete every version and delete marker (bypassing governance
// retention and lifting legal holds on object-lock buckets), then delete the
// bucket.
async function teardownBucket (client, bucket, knownKeys, signal) {
  const warnings = []
  const send = (cmd) => client.send(cmd, { abortSignal: signal })

  // Abort in-flight multipart uploads.
  try {
    let keyMarker, uploadIdMarker
    do {
      const mu = await send(new ListMultipartUploadsCommand({
        Bucket: bucket, KeyMarker: keyMarker, UploadIdMarker: uploadIdMarker
      }))
      for (const u of mu.Uploads ?? []) {
        try {
          await send(new AbortMultipartUploadCommand({ Bucket: bucket, Key: u.Key, UploadId: u.UploadId }))
        } catch (err) {
          warnings.push(`teardown ${bucket}: AbortMultipartUpload ${u.Key}: ${err.message}`)
        }
      }
      keyMarker = mu.IsTruncated ? mu.NextKeyMarker : undefined
      uploadIdMarker = mu.IsTruncated ? mu.NextUploadIdMarker : undefined
    } while (keyMarker !== undefined)
  } catch (err) {
    if (isNoSuchBucket(err)) return warnings // already gone — nothing to do
    warnings.push(`teardown ${bucket}: ListMultipartUploads: ${err.message}`)
  }

  // Delete keys the runner knows it wrote, in case the server's listings
  // miss them (best-effort; the sweep below reports anything that fails).
  for (const key of knownKeys) {
    try {
      await send(new DeleteObjectCommand({ Bucket: bucket, Key: key }))
    } catch { /* the sweep reports residuals */ }
  }

  // AWS rejects BypassGovernanceRetention on buckets without object lock,
  // so only send it (and lift legal holds) where lock is configured.
  const locked = await bucketHasObjectLock(client, bucket, signal)

  try {
    let keyMarker, versionIdMarker
    do {
      const lv = await send(new ListObjectVersionsCommand({
        Bucket: bucket, KeyMarker: keyMarker, VersionIdMarker: versionIdMarker
      }))
      const ids = []
      for (const v of lv.Versions ?? []) {
        if (locked) {
          try {
            await send(new PutObjectLegalHoldCommand({
              Bucket: bucket, Key: v.Key, VersionId: v.VersionId, LegalHold: { Status: 'OFF' }
            }))
          } catch { /* legal holds block deletion even with bypass */ }
        }
        ids.push({ Key: v.Key, VersionId: v.VersionId })
      }
      for (const m of lv.DeleteMarkers ?? []) ids.push({ Key: m.Key, VersionId: m.VersionId })
      for (let i = 0; i < ids.length; i += 1000) {
        const input = { Bucket: bucket, Delete: { Objects: ids.slice(i, i + 1000), Quiet: true } }
        if (locked) input.BypassGovernanceRetention = true
        try {
          const out = await send(new DeleteObjectsCommand(input))
          for (const e of out.Errors ?? []) {
            warnings.push(`teardown ${bucket}: delete ${e.Key} (${e.VersionId ?? ''}): ${e.Message}`)
          }
        } catch (err) {
          warnings.push(`teardown ${bucket}: DeleteObjects: ${err.message}`)
        }
      }
      keyMarker = lv.IsTruncated ? lv.NextKeyMarker : undefined
      versionIdMarker = lv.IsTruncated ? lv.NextVersionIdMarker : undefined
    } while (keyMarker !== undefined)
  } catch (err) {
    warnings.push(`teardown ${bucket}: ListObjectVersions: ${err.message}`)
  }

  try {
    await send(new DeleteBucketCommand({ Bucket: bucket }))
  } catch (err) {
    if (!isNoSuchBucket(err)) warnings.push(`teardown ${bucket}: DeleteBucket: ${err.message}`)
  }
  return warnings
}

async function bucketHasObjectLock (client, bucket, signal) {
  try {
    const out = await client.send(new GetObjectLockConfigurationCommand({ Bucket: bucket }), { abortSignal: signal })
    return out.ObjectLockConfiguration?.ObjectLockEnabled === 'Enabled'
  } catch {
    return false // typically ObjectLockConfigurationNotFoundError
  }
}
