// Capture-path grammar: path = ident ("." ident | "[" digits "]")*,
// evaluated against a generic JSON-like value.

/**
 * Parse validates a capture path and returns its segments
 * ({key} for field access, {index} for array index).
 * @param {string} path
 */
export function parse (path) {
  if (path === '') throw new Error('empty capture path')
  const segs = []
  let i = 0
  const ident = () => {
    const start = i
    while (i < path.length && path[i] !== '.' && path[i] !== '[') {
      if (path[i] === ']') throw new Error(`capture path ${JSON.stringify(path)}: unexpected "]" at ${i}`)
      i++
    }
    if (i === start) throw new Error(`capture path ${JSON.stringify(path)}: empty identifier at ${start}`)
    return path.slice(start, i)
  }
  segs.push({ key: ident() })
  while (i < path.length) {
    switch (path[i]) {
      case '.': {
        i++
        segs.push({ key: ident() })
        break
      }
      case '[': {
        i++
        const close = path.indexOf(']', i)
        if (close < 0) throw new Error(`capture path ${JSON.stringify(path)}: unterminated index`)
        const digits = path.slice(i, close)
        if (!/^\d+$/.test(digits)) throw new Error(`capture path ${JSON.stringify(path)}: bad index ${JSON.stringify(digits)}`)
        segs.push({ index: Number(digits) })
        i = close + 1
        break
      }
      default:
        throw new Error(`capture path ${JSON.stringify(path)}: unexpected ${JSON.stringify(path[i])} at ${i}`)
    }
  }
  return segs
}

/**
 * Evaluate path against v and return the addressed value.
 * @param {unknown} v
 * @param {string} path
 */
export function get (v, path) {
  let cur = v
  for (const seg of parse(path)) {
    if (seg.index !== undefined) {
      if (!Array.isArray(cur)) throw new Error(`capture path ${JSON.stringify(path)}: [${seg.index}] applied to non-array`)
      if (seg.index >= cur.length) throw new Error(`capture path ${JSON.stringify(path)}: index ${seg.index} out of range (len ${cur.length})`)
      cur = cur[seg.index]
      continue
    }
    if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) {
      throw new Error(`capture path ${JSON.stringify(path)}: field ${JSON.stringify(seg.key)} applied to non-object`)
    }
    if (!(seg.key in cur)) throw new Error(`capture path ${JSON.stringify(path)}: no field ${JSON.stringify(seg.key)} in response`)
    cur = cur[seg.key]
  }
  return cur
}

/**
 * Evaluate path and render the result as the string form used for
 * ${cap.<name>} substitution.
 * @param {unknown} v
 * @param {string} path
 */
export function getString (v, path) {
  const got = get(v, path)
  switch (typeof got) {
    case 'string':
      return got
    case 'boolean':
      return String(got)
    case 'number':
      return String(got)
    default:
      if (got === null) throw new Error(`capture path ${JSON.stringify(path)}: value is null`)
      throw new Error(`capture path ${JSON.stringify(path)}: cannot capture ${typeof got} as a string`)
  }
}
