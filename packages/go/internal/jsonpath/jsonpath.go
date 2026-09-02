// Package jsonpath implements the capture-path grammar:
//
//	path = ident ("." ident | "[" digits "]")*
//
// evaluated against a generic JSON-like value (map[string]any / []any).
package jsonpath

import (
	"fmt"
	"strconv"
	"strings"
)

type segment struct {
	key   string // field access when index < 0
	index int
}

// Parse validates a capture path and returns its segments.
func Parse(path string) ([]segment, error) {
	if path == "" {
		return nil, fmt.Errorf("empty capture path")
	}
	var segs []segment
	i := 0
	ident := func() (string, error) {
		start := i
		for i < len(path) && path[i] != '.' && path[i] != '[' {
			c := path[i]
			if c == ']' {
				return "", fmt.Errorf("capture path %q: unexpected %q at %d", path, c, i)
			}
			i++
		}
		if i == start {
			return "", fmt.Errorf("capture path %q: empty identifier at %d", path, start)
		}
		return path[start:i], nil
	}
	id, err := ident()
	if err != nil {
		return nil, err
	}
	segs = append(segs, segment{key: id, index: -1})
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			id, err := ident()
			if err != nil {
				return nil, err
			}
			segs = append(segs, segment{key: id, index: -1})
		case '[':
			i++
			close := strings.IndexByte(path[i:], ']')
			if close < 0 {
				return nil, fmt.Errorf("capture path %q: unterminated index", path)
			}
			n, err := strconv.Atoi(path[i : i+close])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("capture path %q: bad index %q", path, path[i:i+close])
			}
			segs = append(segs, segment{index: n})
			i += close + 1
		default:
			return nil, fmt.Errorf("capture path %q: unexpected %q at %d", path, path[i], i)
		}
	}
	return segs, nil
}

// Get evaluates path against v and returns the addressed value.
func Get(v any, path string) (any, error) {
	segs, err := Parse(path)
	if err != nil {
		return nil, err
	}
	cur := v
	for _, s := range segs {
		if s.index >= 0 {
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("capture path %q: [%d] applied to non-array %T", path, s.index, cur)
			}
			if s.index >= len(arr) {
				return nil, fmt.Errorf("capture path %q: index %d out of range (len %d)", path, s.index, len(arr))
			}
			cur = arr[s.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("capture path %q: field %q applied to non-object %T", path, s.key, cur)
		}
		val, ok := obj[s.key]
		if !ok {
			return nil, fmt.Errorf("capture path %q: no field %q in response", path, s.key)
		}
		cur = val
	}
	return cur, nil
}

// GetString evaluates path and renders the result as the string form used for
// ${cap.<name>} substitution.
func GetString(v any, path string) (string, error) {
	got, err := Get(v, path)
	if err != nil {
		return "", err
	}
	switch t := got.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case fmt.Stringer:
		return t.String(), nil
	case nil:
		return "", fmt.Errorf("capture path %q: value is null", path)
	default:
		return "", fmt.Errorf("capture path %q: cannot capture %T as a string", path, got)
	}
}
