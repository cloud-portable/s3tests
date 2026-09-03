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

/** Drop vectors with any of the given ids (the skip-list filter). */
export const excludeIds = (...i) => (v) => !i.includes(v.id)
