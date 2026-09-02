// Package match implements the vector matcher semantics: scalar equality,
// recursive subset objects, exact-length ordered arrays, assertion objects
// ($exists/$absent/$eq/$ne/$matches/$length/$contains), plus the header,
// error and body (content-descriptor / digest) expectation forms.
package match

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Mismatch describes one failed check. Path is relative to the matched root
// (e.g. "Contents[0].Key").
type Mismatch struct {
	Path     string
	Expected string
	Actual   string
}

// Value matches a decoded expected matcher against an actual value.
// present=false means the addressed value does not exist in the response
// (distinct from existing with a null/empty value).
func Value(path string, expected, actual any, present bool) []Mismatch {
	if m, ok := expected.(map[string]any); ok {
		if isAssertion(m) {
			return assertion(path, m, actual, present)
		}
		if !present {
			return miss(path, expected, nil, false)
		}
		am, ok := actual.(map[string]any)
		if !ok {
			return miss(path, expected, actual, true)
		}
		var out []Mismatch
		for k, ev := range m {
			av, ok := am[k]
			out = append(out, Value(join(path, k), ev, av, ok)...)
		}
		return out
	}
	if ea, ok := expected.([]any); ok {
		if !present {
			return miss(path, expected, nil, false)
		}
		aa, ok := actual.([]any)
		if !ok {
			return miss(path, expected, actual, true)
		}
		if len(ea) != len(aa) {
			return []Mismatch{{Path: path, Expected: fmt.Sprintf("array of length %d", len(ea)), Actual: fmt.Sprintf("array of length %d", len(aa))}}
		}
		var out []Mismatch
		for i, ev := range ea {
			out = append(out, Value(fmt.Sprintf("%s[%d]", path, i), ev, aa[i], true)...)
		}
		return out
	}
	// Scalar literal: exact equality.
	if !present {
		return miss(path, expected, nil, false)
	}
	if !scalarEqual(expected, actual) {
		return miss(path, expected, actual, true)
	}
	return nil
}

func isAssertion(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	for k := range m {
		if !strings.HasPrefix(k, "$") {
			return false
		}
	}
	return true
}

func assertion(path string, m map[string]any, actual any, present bool) []Mismatch {
	var out []Mismatch
	for op, arg := range m {
		switch op {
		case "$exists":
			if want, _ := arg.(bool); present != want {
				out = append(out, Mismatch{Path: path, Expected: fmt.Sprintf("exists: %v", want), Actual: presence(actual, present)})
			}
		case "$absent":
			if want, _ := arg.(bool); present == want {
				out = append(out, Mismatch{Path: path, Expected: fmt.Sprintf("absent: %v", want), Actual: presence(actual, present)})
			}
		case "$eq":
			if !present || !literalEqual(arg, actual) {
				out = append(out, miss(path, arg, actual, present)...)
			}
		case "$ne":
			// Scalar inequality (compared after placeholder interpolation).
			// An absent value fails: the assertion demands a differing value.
			if !present || scalarEqual(arg, actual) {
				out = append(out, Mismatch{Path: path, Expected: "not equal to " + render(arg), Actual: presence(actual, present)})
			}
		case "$matches":
			pat, _ := arg.(string)
			s, ok := scalarString(actual)
			if !present || !ok {
				out = append(out, Mismatch{Path: path, Expected: "matches " + strconv.Quote(pat), Actual: presence(actual, present)})
				continue
			}
			matched, err := regexMatch(pat, s)
			if err != nil {
				out = append(out, Mismatch{Path: path, Expected: "matches " + strconv.Quote(pat), Actual: "invalid regex: " + err.Error()})
			} else if !matched {
				out = append(out, Mismatch{Path: path, Expected: "matches " + strconv.Quote(pat), Actual: strconv.Quote(s)})
			}
		case "$length":
			want, ok := toFloat(arg)
			n, lok := lengthOf(actual)
			if !present || !ok || !lok || float64(n) != want {
				out = append(out, Mismatch{Path: path, Expected: fmt.Sprintf("length %v", arg), Actual: lengthActual(actual, present)})
			}
		case "$contains":
			aa, ok := actual.([]any)
			if !present || !ok {
				out = append(out, Mismatch{Path: path, Expected: "array containing " + render(arg), Actual: presence(actual, present)})
				continue
			}
			found := false
			for _, el := range aa {
				if len(Value(path, arg, el, true)) == 0 {
					found = true
					break
				}
			}
			if !found {
				out = append(out, Mismatch{Path: path, Expected: "some element matching " + render(arg), Actual: fmt.Sprintf("no match among %d element(s)", len(aa))})
			}
		default:
			out = append(out, Mismatch{Path: path, Expected: "known assertion", Actual: "unknown assertion operator " + op})
		}
	}
	return out
}

// literalEqual is deep equality with numeric normalization ($eq semantics).
func literalEqual(expected, actual any) bool {
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok || len(a) != len(e) {
			return false
		}
		for k, ev := range e {
			av, ok := a[k]
			if !ok || !literalEqual(ev, av) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(e) {
			return false
		}
		for i := range e {
			if !literalEqual(e[i], a[i]) {
				return false
			}
		}
		return true
	default:
		return scalarEqual(expected, actual)
	}
}

func scalarEqual(expected, actual any) bool {
	if en, ok := toFloat(expected); ok {
		an, ok := toFloat(actual)
		return ok && en == an
	}
	switch e := expected.(type) {
	case string:
		a, ok := actual.(string)
		return ok && a == e
	case bool:
		a, ok := actual.(bool)
		return ok && a == e
	case nil:
		return actual == nil
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	}
	return "", false
}

func lengthOf(v any) (int, bool) {
	switch t := v.(type) {
	case []any:
		return len(t), true
	case string:
		return len(t), true
	}
	return 0, false
}

func lengthActual(v any, present bool) string {
	if !present {
		return "(absent)"
	}
	if n, ok := lengthOf(v); ok {
		return fmt.Sprintf("length %d", n)
	}
	return fmt.Sprintf("%T (no length)", v)
}

func presence(v any, present bool) string {
	if !present {
		return "(absent)"
	}
	return render(v)
}

func miss(path string, expected, actual any, present bool) []Mismatch {
	return []Mismatch{{Path: path, Expected: render(expected), Actual: presence(actual, present)}}
}

func render(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 256 {
		s = s[:256] + "…"
	}
	return s
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// $matches patterns are, per the spec, a portable subset valid in both
// ECMA-262 and RE2 (no lookarounds, no backreferences), so Go's native
// regexp suffices. Matching is unanchored.
var (
	regexMu    sync.Mutex
	regexCache = map[string]*regexp.Regexp{}
)

func regexMatch(pattern, s string) (bool, error) {
	regexMu.Lock()
	re, ok := regexCache[pattern]
	var err error
	if !ok {
		re, err = regexp.Compile(pattern)
		if err == nil {
			regexCache[pattern] = re
		}
	}
	regexMu.Unlock()
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// CompileRegex reports whether a $matches pattern compiles (used by the
// offline corpus smoke test).
func CompileRegex(pattern string) error {
	_, err := regexp.Compile(pattern)
	return err
}

// Headers matches expect.headers (lowercase name -> matcher) against actual
// response headers. Multi-valued actual headers match on the first value.
func Headers(expected map[string]json.RawMessage, hdr http.Header) ([]Mismatch, error) {
	var out []Mismatch
	for name, raw := range expected {
		matcher, err := decode(raw)
		if err != nil {
			return nil, fmt.Errorf("expect.headers[%s]: %w", name, err)
		}
		vals := hdr.Values(name)
		var actual any
		present := len(vals) > 0
		if present {
			actual = vals[0]
		}
		out = append(out, Value("headers."+name, matcher, actual, present)...)
	}
	return out, nil
}

// Error matches expect.error (a code string, or {code, message}) against the
// observed error code/message.
func Error(expected json.RawMessage, code, message string) ([]Mismatch, error) {
	v, err := decode(expected)
	if err != nil {
		return nil, fmt.Errorf("expect.error: %w", err)
	}
	switch e := v.(type) {
	case string:
		if code != e {
			return []Mismatch{{Path: "error", Expected: e, Actual: orEmpty(code)}}, nil
		}
		return nil, nil
	case map[string]any:
		var out []Mismatch
		if want, ok := e["code"].(string); ok && code != want {
			out = append(out, Mismatch{Path: "error.code", Expected: want, Actual: orEmpty(code)})
		}
		if msg, ok := e["message"]; ok {
			out = append(out, Value("error.message", msg, message, message != "")...)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expect.error: unsupported form %s", render(v))
	}
}

// ContentResolver resolves a decoded content descriptor's $data reference.
type ContentResolver func(name string) ([]byte, error)

// Body matches expect.body — either a content descriptor (exact bytes) or a
// digest assertion {$size,$md5,$sha256} — against the actual body bytes.
func Body(expected json.RawMessage, body []byte, resolve ContentResolver) ([]Mismatch, error) {
	v, err := decode(expected)
	if err != nil {
		return nil, fmt.Errorf("expect.body: %w", err)
	}
	if m, ok := v.(map[string]any); ok && isAssertion(m) {
		if _, isData := m["$data"]; !isData {
			if _, isB64 := m["$base64"]; !isB64 {
				return digestBody(m, body)
			}
		}
	}
	want, err := Content(expected, resolve)
	if err != nil {
		return nil, fmt.Errorf("expect.body: %w", err)
	}
	if !bytes.Equal(want, body) {
		return []Mismatch{{Path: "body", Expected: summarize(want), Actual: summarize(body)}}, nil
	}
	return nil, nil
}

func digestBody(m map[string]any, body []byte) ([]Mismatch, error) {
	var out []Mismatch
	for op, arg := range m {
		switch op {
		case "$size":
			want, ok := toFloat(arg)
			if !ok || want != float64(len(body)) {
				out = append(out, Mismatch{Path: "body.$size", Expected: render(arg), Actual: strconv.Itoa(len(body))})
			}
		case "$md5":
			sum := md5.Sum(body)
			if got := hex.EncodeToString(sum[:]); got != arg {
				out = append(out, Mismatch{Path: "body.$md5", Expected: render(arg), Actual: got})
			}
		case "$sha256":
			sum := sha256.Sum256(body)
			if got := hex.EncodeToString(sum[:]); got != arg {
				out = append(out, Mismatch{Path: "body.$sha256", Expected: render(arg), Actual: got})
			}
		default:
			return nil, fmt.Errorf("expect.body: unknown digest assertion %s", op)
		}
	}
	return out, nil
}

// Content decodes a content descriptor — plain string (UTF-8), {"$data": name}
// or {"$base64": "..."} — into bytes.
func Content(raw json.RawMessage, resolve ContentResolver) ([]byte, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	return ContentValue(v, resolve)
}

// ContentValue is Content over an already-decoded JSON value.
func ContentValue(v any, resolve ContentResolver) ([]byte, error) {
	switch t := v.(type) {
	case string:
		return []byte(t), nil
	case map[string]any:
		if name, ok := t["$data"].(string); ok && len(t) == 1 {
			if resolve == nil {
				return nil, fmt.Errorf("content descriptor references dataset %q but the vector declares no data", name)
			}
			return resolve(name)
		}
		if enc, ok := t["$base64"].(string); ok && len(t) == 1 {
			b, err := base64decode(enc)
			if err != nil {
				return nil, fmt.Errorf("bad $base64 content: %w", err)
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("invalid content descriptor: %s", render(v))
}

func summarize(b []byte) string {
	sum := md5.Sum(b)
	s := fmt.Sprintf("%d bytes, md5 %s", len(b), hex.EncodeToString(sum[:]))
	if len(b) <= 64 && isPrintable(b) {
		s += fmt.Sprintf(" (%q)", b)
	}
	return s
}

func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func orEmpty(s string) string {
	if s == "" {
		return "(no error)"
	}
	return s
}

func decode(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Decode exposes UseNumber-decoding for callers that pre-decode matchers.
func Decode(raw json.RawMessage) (any, error) { return decode(raw) }

func base64decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
