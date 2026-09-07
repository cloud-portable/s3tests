// Vector selection as composable functions. applyFilters ANDs its filters;
// exclude filters return false for matches, so ANDing drops them.

/**
 * The vectors selected by every filter (logical AND), preserving order. With
 * no filters the input is returned unchanged.
 * @param {object[]} vectors
 * @param {...(v: object) => boolean} filters
 */
export function applyFilters (vectors, ...filters) {
  if (filters.length === 0) return vectors
  return vectors.filter((v) => filters.every((f) => f(v)))
}

/** Vectors in any of the given feature groups. */
export const groups = (...names) => (v) => names.includes(v.group)

/** Vectors carrying at least one of the given tags. */
export const tags = (...t) => (v) => t.some((tag) => (v.tags ?? []).includes(tag))

/** Vectors with any of the given ids. */
export const ids = (...i) => (v) => i.includes(v.id)

/** Drop vectors in any of the given feature groups. */
export const excludeGroups = (...names) => (v) => !names.includes(v.group)

/** Drop vectors carrying any of the given tags. */
export const excludeTags = (...t) => (v) => !t.some((tag) => (v.tags ?? []).includes(tag))

/**
 * Drop vectors with any of the given ids. Dropped vectors leave no trace in
 * results; to keep them visible as skipped, pass skip rules to run().
 */
export const excludeIds = (...i) => (v) => !i.includes(v.id)

/**
 * True if s matches a glob pattern whose only metacharacter is '*' (any run of
 * characters, including empty); every other character is literal. Kept
 * identical across the go/js/python runners.
 */
const globMatch = (pattern, s) => {
  const parts = pattern.split('*')
  if (parts.length === 1) return s === pattern // no wildcard: exact match
  if (!s.startsWith(parts[0])) return false
  let rest = s.slice(parts[0].length)
  for (const part of parts.slice(1, -1)) {
    const i = rest.indexOf(part)
    if (i < 0) return false
    rest = rest.slice(i + part.length)
  }
  return rest.endsWith(parts[parts.length - 1])
}

const anyTagMatches = (tags, patterns) => (tags ?? []).some((t) => patterns.some((p) => globMatch(p, t)))

/**
 * Vectors carrying at least one tag matching any of the given glob patterns.
 * A pattern uses '*' as a wildcard for any run of characters; tags never
 * contain '*', so a pattern without one is an exact match. Example:
 * tagsMatching('quirk:*') selects every quirk-tagged vector.
 */
export const tagsMatching = (...patterns) => (v) => anyTagMatches(v.tags, patterns)

/** Drop vectors carrying any tag matching any of the given glob patterns (e.g. 'quirk:*'). */
export const excludeTagsMatching = (...patterns) => (v) => !anyTagMatches(v.tags, patterns)
