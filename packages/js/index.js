// @cloud-portable/s3tests — runner for the language-independent S3 API
// compatibility test vectors. Executes `api`-kind vectors against an S3
// endpoint and streams one result per vector with outcome `pass`, `fail`,
// `blocked` or `skipped`.
//
//   import { Runner, vectors, applyFilters, groups, tags } from '@cloud-portable/s3tests'
//
//   const runner = new Runner({ endpoint, credentials: { accessKeyId, secretAccessKey } })
//   const selected = applyFilters(vectors(), groups('object-crud'), tags('tier-1'))
//   for await (const res of runner.run(selected)) console.log(res.outcome, res.id)
//
// run() executes exactly the vectors it is given: applyFilters composes the
// built-in group/tag/id filters with any custom predicate, but any reduction
// of the vectors() slice works. Vectors dropped this way leave no trace in
// the results; to record vectors as skipped instead — keeping reports
// comparable across runs — pass skip rules to run():
//
//   runner.run(selected, { skip: [skip('known bug #123', ids('multipart-0013'))] })
//
// Signing-kind corpus vectors are out of scope and never loaded.

import { all } from '@cloud-portable/s3vectors'

export { Runner } from './lib/runner.js'
export { defaultProvisioner } from './lib/provision.js'
export {
  applyFilters, groups, tags, ids, excludeGroups, excludeTags, excludeIds
} from './lib/filter.js'
export { skip } from './lib/skip.js'

/**
 * Every api-kind vector in the corpus, in manifest order. The vectors are
 * the corpus package's shared, cached objects — treat them as read-only.
 * @returns {object[]}
 */
export function vectors () {
  const out = []
  for (const file of all()) {
    for (const v of file.vectors) {
      if (v.kind === 'api') out.push(v)
    }
  }
  return out
}
