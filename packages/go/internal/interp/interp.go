// Package interp implements the vector placeholder grammar over JSON values:
// ${env.*}, ${res.<handle>.<attr>}, ${cap.<name>} and ${data.<name>.<field>},
// with $${ escaping. An unresolvable placeholder is a vector-definition error.
package interp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// DataResolver resolves ${data.<name>.<field>} placeholders (see internal/vdata).
type DataResolver func(name, field string) (string, error)

// Scope holds the values placeholders resolve against while a vector runs.
type Scope struct {
	Env  map[string]string            // endpoint, region
	Res  map[string]map[string]string // handle -> attr -> value
	Cap  map[string]string            // capture name -> value
	Data DataResolver                 // nil => any ${data.*} is unresolvable
}

// String interpolates every placeholder in s, or errors on the first
// unresolvable one (the raw text must never be sent).
func (sc *Scope) String(s string) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(s[i:], "${") {
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated placeholder in %q", s)
			}
			val, err := sc.resolve(s[i+2 : i+end])
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			i += end + 1
			continue
		}
		// Any other $ is literal as-is.
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}

func (sc *Scope) resolve(expr string) (string, error) {
	ns, path, ok := strings.Cut(expr, ".")
	if !ok {
		return "", fmt.Errorf("unresolvable placeholder ${%s}: missing path", expr)
	}
	switch ns {
	case "env":
		if v, ok := sc.Env[path]; ok {
			return v, nil
		}
	case "res":
		handle, attr, ok := strings.Cut(path, ".")
		if !ok {
			return "", fmt.Errorf("unresolvable placeholder ${%s}: want res.<handle>.<attr>", expr)
		}
		if attrs, ok := sc.Res[handle]; ok {
			if v, ok := attrs[attr]; ok {
				return v, nil
			}
			return "", fmt.Errorf("unresolvable placeholder ${%s}: resource %q has no attribute %q", expr, handle, attr)
		}
	case "cap":
		if v, ok := sc.Cap[path]; ok {
			return v, nil
		}
	case "data":
		name, field, ok := strings.Cut(path, ".")
		if !ok {
			return "", fmt.Errorf("unresolvable placeholder ${%s}: want data.<name>.<field>", expr)
		}
		if sc.Data == nil {
			break
		}
		v, err := sc.Data(name, field)
		if err != nil {
			return "", fmt.Errorf("unresolvable placeholder ${%s}: %w", expr, err)
		}
		return v, nil
	default:
		return "", fmt.Errorf("unresolvable placeholder ${%s}: unknown namespace %q", expr, ns)
	}
	return "", fmt.Errorf("unresolvable placeholder ${%s}", expr)
}

// Raw interpolates every JSON string value inside raw, returning a new
// message; the input (typically shared corpus data) is never mutated.
// Object keys are not interpolated (the corpus never places placeholders in
// keys; capture names and header names are structural).
func (sc *Scope) Raw(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON in vector: %w", err)
	}
	out, err := sc.value(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// Value interpolates every string inside a decoded JSON value.
func (sc *Scope) Value(v any) (any, error) { return sc.value(v) }

func (sc *Scope) value(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return sc.String(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			ie, err := sc.value(e)
			if err != nil {
				return nil, err
			}
			out[k] = ie
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			ie, err := sc.value(e)
			if err != nil {
				return nil, err
			}
			out[i] = ie
		}
		return out, nil
	default:
		return v, nil
	}
}
