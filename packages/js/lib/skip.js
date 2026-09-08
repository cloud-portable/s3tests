// Skip rules for Runner.run(): record vectors as `skipped` (with a reason)
// instead of executing them. Unlike dropping vectors with applyFilters
// beforehand, skipped vectors still appear in the result stream — outcome
// 'skipped', the reason, no steps, zero duration — so reports stay
// comparable across runs and document what was deliberately not exercised.
//
// A skip rule is any function `(vector) => string | undefined`: a string
// (even an empty one) is the reason to skip the vector; undefined lets it
// run. skip() builds one from a reason and filters; a hand-written rule is
// the general form for reasons that vary per vector:
//
//   runner.run(selected, {
//     skip: [
//       skip('known server bug #123', ids('multipart-0013')),
//       (v) => knownIssues[v.id] // map of id -> issue link, or undefined
//     ]
//   })

/**
 * A rule skipping vectors matched by every given filter (logical AND,
 * exactly as applyFilters selects) with the given reason. With no filters
 * every vector is skipped (a dry run that lists the selection). Several
 * rules compose: the first one matching a vector supplies its reason.
 * @param {string} reason
 * @param {...(v: object) => boolean} filters
 * @returns {(v: object) => string | undefined}
 */
export const skip = (reason, ...filters) => (v) => (filters.every((f) => f(v)) ? reason : undefined)

/**
 * The reason the first matching rule gives for skipping the vector, or
 * undefined when no rule matches.
 * @param {Array<(v: object) => string | undefined>} rules
 * @param {object} vector
 * @returns {string | undefined}
 */
export function skipReason (rules, vector) {
  for (const rule of rules) {
    const reason = rule(vector)
    if (typeof reason === 'string') return reason
  }
  return undefined
}

import { tagsMatching } from './filter.js'

/**
 * Glob matching a vector whose expectation a general-purpose AWS-tracking
 * target does not reproduce (quirk:not-aws, quirk:directory-bucket,
 * quirk:us-east-1-legacy, …). Such vectors contradict the baseline vectors, so
 * run() skips them by default via defaultSkip; noSkip / noSkipMatching opt them
 * back in.
 */
export const quirkTagGlob = 'quirk:*'

export const quirkSkipReason = 'quirk vector skipped by default (run it with noSkip/noSkipMatching)'

/**
 * The default skip rule: skip quirk vectors. run() prepends it before the
 * caller's explicit rules; a noSkip / noSkipMatching unskip opts them back in.
 * @type {(v: object) => string | undefined}
 */
export const defaultSkip = skip(quirkSkipReason, tagsMatching(quirkTagGlob))
