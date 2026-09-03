// Vector placeholder interpolation: ${env.*}, ${res.<handle>.<attr>},
// ${cap.<name>} and ${data.<name>.<field>}, with $${ escaping. An
// unresolvable placeholder is a vector-definition error.

/**
 * Scope holds the values placeholders resolve against while a vector runs.
 * `data` is a resolver `(name, field) => string` (see vdata.js).
 */
export class Scope {
  /** @param {{env?: object, res?: object, cap?: object, data?: (name: string, field: string) => string}} init */
  constructor ({ env = {}, res = {}, cap = {}, data = null } = {}) {
    this.env = env
    this.res = res
    this.cap = cap
    this.data = data
  }

  /**
   * Interpolate every placeholder in s, or throw on the first unresolvable
   * one (the raw text must never be sent).
   * @param {string} s
   */
  string (s) {
    if (!s.includes('$')) return s
    let out = ''
    for (let i = 0; i < s.length;) {
      const c = s[i]
      if (c !== '$') {
        out += c
        i++
        continue
      }
      if (s.startsWith('$${', i)) {
        out += '${'
        i += 3
        continue
      }
      if (s.startsWith('${', i)) {
        const end = s.indexOf('}', i)
        if (end < 0) throw new Error(`unterminated placeholder in ${JSON.stringify(s)}`)
        out += this.#resolve(s.slice(i + 2, end))
        i = end + 1
        continue
      }
      // Any other $ is literal as-is.
      out += c
      i++
    }
    return out
  }

  /** @param {string} expr */
  #resolve (expr) {
    const dot = expr.indexOf('.')
    if (dot < 0) throw new Error(`unresolvable placeholder \${${expr}}: missing path`)
    const ns = expr.slice(0, dot)
    const path = expr.slice(dot + 1)
    switch (ns) {
      case 'env':
        if (path in this.env) return this.env[path]
        break
      case 'res': {
        const d = path.indexOf('.')
        if (d < 0) throw new Error(`unresolvable placeholder \${${expr}}: want res.<handle>.<attr>`)
        const handle = path.slice(0, d)
        const attr = path.slice(d + 1)
        const attrs = this.res[handle]
        if (attrs) {
          if (attr in attrs) return attrs[attr]
          throw new Error(`unresolvable placeholder \${${expr}}: resource ${JSON.stringify(handle)} has no attribute ${JSON.stringify(attr)}`)
        }
        break
      }
      case 'cap':
        if (path in this.cap) return this.cap[path]
        break
      case 'data': {
        const d = path.indexOf('.')
        if (d < 0) throw new Error(`unresolvable placeholder \${${expr}}: want data.<name>.<field>`)
        if (!this.data) break
        try {
          return this.data(path.slice(0, d), path.slice(d + 1))
        } catch (err) {
          throw new Error(`unresolvable placeholder \${${expr}}: ${err.message}`)
        }
      }
      default:
        throw new Error(`unresolvable placeholder \${${expr}}: unknown namespace ${JSON.stringify(ns)}`)
    }
    throw new Error(`unresolvable placeholder \${${expr}}`)
  }

  /**
   * Interpolate every string inside a decoded JSON value, returning a new
   * value; the input (typically shared corpus data) is never mutated. Object
   * keys are not interpolated.
   * @param {unknown} v
   * @returns {unknown}
   */
  value (v) {
    if (typeof v === 'string') return this.string(v)
    if (Array.isArray(v)) return v.map((e) => this.value(e))
    if (v !== null && typeof v === 'object') {
      const out = {}
      for (const [k, e] of Object.entries(v)) out[k] = this.value(e)
      return out
    }
    return v
  }
}
