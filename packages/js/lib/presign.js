// Presigned-URL steps: mint with the SDK presigner, execute with fetch.
// Parity note vs the Go runner: the JS presigner hoists signable headers
// into X-Amz-* query parameters rather than returning headers to send —
// both are valid SigV4 presigned forms and the corpus's presigned vectors
// carry no header-bound params.

import * as clientS3 from '@aws-sdk/client-s3'
import { getSignedUrl } from '@aws-sdk/s3-request-presigner'
import { buildInput } from './coerce.js'
import { unsupportedError } from './dispatch.js'

/** Whether the operation can be presigned (any v3 command can). */
export function presignSupported (name) {
  return typeof clientS3[name + 'Command'] === 'function'
}

/**
 * Mint a presigned request and execute it. Body bytes are held aside and
 * sent by us (S3 presigned requests use UNSIGNED-PAYLOAD).
 * @returns {Promise<{status: number, headers: Record<string,string>, body: Uint8Array}>}
 */
export async function presignAndExecute (client, name, params, resolveData, expiresIn, signal) {
  const Command = clientS3[name + 'Command']
  if (typeof Command !== 'function') throw unsupportedError(name)
  const { input, body } = buildInput(params ?? {}, resolveData)
  const method = presignMethod(name)
  if (body !== null) delete input.Body // UNSIGNED-PAYLOAD: sent at execution time
  const url = await getSignedUrl(client, new Command(input), expiresIn > 0 ? { expiresIn } : {})

  const resp = await fetch(url, {
    method,
    body: body !== null && method !== 'GET' && method !== 'HEAD' ? body : undefined,
    redirect: 'manual',
    signal
  })
  const bytes = new Uint8Array(await resp.arrayBuffer())
  const headers = {}
  for (const [k, v] of resp.headers.entries()) headers[k.toLowerCase()] = v
  return { status: resp.status, headers, body: bytes }
}

// The HTTP method behind each presignable operation the corpus uses.
function presignMethod (name) {
  if (name.startsWith('Get') || name.startsWith('List') || name.startsWith('Head')) {
    return name.startsWith('Head') ? 'HEAD' : 'GET'
  }
  if (name.startsWith('Delete')) return 'DELETE'
  return 'PUT'
}
