package match

import (
	"encoding/json"
	"net/http"
	"testing"
)

func dec(t *testing.T, s string) any {
	t.Helper()
	v, err := Decode(json.RawMessage(s))
	if err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

func TestScalars(t *testing.T) {
	cases := []struct {
		expected string
		actual   any
		ok       bool
	}{
		{`"a"`, "a", true},
		{`"a"`, "b", false},
		{`5`, int64(5), true},
		{`5`, float64(5), true},
		{`5`, "5", false}, // string is not a number
		{`true`, true, true},
		{`null`, nil, true},
		{`10485760`, int64(10485760), true},
	}
	for _, c := range cases {
		got := Value("x", dec(t, c.expected), c.actual, true)
		if (len(got) == 0) != c.ok {
			t.Errorf("Value(%s, %v): mismatches %v, want ok=%v", c.expected, c.actual, got, c.ok)
		}
	}
}

func TestSubsetObject(t *testing.T) {
	actual := map[string]any{"Key": "a", "Size": int64(5), "Extra": "ignored"}
	if ms := Value("", dec(t, `{"Key":"a","Size":5}`), actual, true); len(ms) != 0 {
		t.Errorf("subset should match: %v", ms)
	}
	ms := Value("", dec(t, `{"Key":"b"}`), actual, true)
	if len(ms) != 1 || ms[0].Path != "Key" {
		t.Errorf("want single Key mismatch, got %v", ms)
	}
	// Missing expected field.
	if ms := Value("", dec(t, `{"Nope":"x"}`), actual, true); len(ms) != 1 {
		t.Errorf("missing field should mismatch: %v", ms)
	}
}

func TestArrays(t *testing.T) {
	actual := []any{map[string]any{"Key": "a"}, map[string]any{"Key": "b"}}
	if ms := Value("", dec(t, `[{"Key":"a"},{"Key":"b"}]`), actual, true); len(ms) != 0 {
		t.Errorf("ordered match failed: %v", ms)
	}
	if ms := Value("", dec(t, `[{"Key":"b"},{"Key":"a"}]`), actual, true); len(ms) == 0 {
		t.Error("order must matter")
	}
	if ms := Value("", dec(t, `[{"Key":"a"}]`), actual, true); len(ms) == 0 {
		t.Error("length must be exact")
	}
}

func TestAssertions(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   any
		present  bool
		ok       bool
	}{
		{"exists yes", `{"$exists":true}`, "v", true, true},
		{"exists no", `{"$exists":true}`, nil, false, false},
		{"absent yes", `{"$absent":true}`, nil, false, true},
		{"absent no", `{"$absent":true}`, "v", true, false},
		{"eq literal assertion-looking", `{"$eq":{"$exists":true}}`, map[string]any{"$exists": true}, true, true},
		{"eq scalar", `{"$eq":5}`, int64(5), true, true},
		{"matches", `{"$matches":"-2\"$"}`, `"abc-2"`, true, true},
		{"matches no", `{"$matches":"-2\"$"}`, `"abc-3"`, true, false},
		{"matches number actual", `{"$matches":"^20"}`, int64(2026), true, true},
		{"length arr", `{"$length":2}`, []any{"a", "b"}, true, true},
		{"length str", `{"$length":3}`, "abc", true, true},
		{"length wrong", `{"$length":3}`, []any{"a"}, true, false},
		{"contains", `{"$contains":{"Key":"b"}}`, []any{map[string]any{"Key": "a"}, map[string]any{"Key": "b", "X": 1}}, true, true},
		{"contains no", `{"$contains":"z"}`, []any{"a", "b"}, true, false},
		{"combined", `{"$exists":true,"$matches":"^a"}`, "abc", true, true},
		{"ne differs", `{"$ne":"\"abc\""}`, `"def"`, true, true},
		{"ne equal", `{"$ne":"\"abc\""}`, `"abc"`, true, false},
		{"ne absent", `{"$ne":"\"abc\""}`, nil, false, false},
		{"ne number equal", `{"$ne":5}`, int64(5), true, false},
		{"ne combined", `{"$ne":"\"s\"","$matches":"-"}`, `"comp-2"`, true, true},
		{"ne combined equal", `{"$ne":"\"comp-2\"","$matches":"-"}`, `"comp-2"`, true, false},
		{"ne combined no dash", `{"$ne":"\"s\"","$matches":"-"}`, `"nodash"`, true, false},
	}
	for _, c := range cases {
		ms := Value("x", dec(t, c.expected), c.actual, c.present)
		if (len(ms) == 0) != c.ok {
			t.Errorf("%s: mismatches %v, want ok=%v", c.name, ms, c.ok)
		}
	}
}

// $matches patterns are the portable ECMA-262 ∩ RE2 subset per the spec: they
// compile with Go's native regexp, and lookarounds are rejected (the corpus
// expresses ETag inequality with $ne instead).
func TestPortableRegexes(t *testing.T) {
	for _, pat := range []string{`-2"$`, `^\d{4}-\d{2}-\d{2}`, `^(STANDARD|REDUCED_REDUNDANCY|)$`} {
		if err := CompileRegex(pat); err != nil {
			t.Errorf("CompileRegex(%q): %v", pat, err)
		}
	}
	if err := CompileRegex(`^(?!"x"$)`); err == nil {
		t.Error("negative lookahead should not compile under native regexp")
	}
	// An uncompilable pattern must surface as a mismatch, never pass silently.
	if ms := Value("ETag", dec(t, `{"$matches":"^(?!\"abc\"$)"}`), `"def"`, true); len(ms) == 0 {
		t.Error("uncompilable pattern should produce a mismatch")
	}
}

func TestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Range", "bytes 0-4/10")
	h.Set("X-Amz-Request-Id", "R1")
	exp := map[string]json.RawMessage{
		"content-range":    json.RawMessage(`"bytes 0-4/10"`),
		"x-amz-request-id": json.RawMessage(`{"$exists":true}`),
		"x-amz-missing":    json.RawMessage(`{"$absent":true}`),
	}
	ms, err := Headers(exp, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 {
		t.Errorf("headers: %v", ms)
	}
	exp["content-range"] = json.RawMessage(`"bytes 0-5/10"`)
	if ms, _ := Headers(exp, h); len(ms) != 1 {
		t.Errorf("want 1 mismatch, got %v", ms)
	}
}

func TestError(t *testing.T) {
	if ms, err := Error(json.RawMessage(`"NoSuchKey"`), "NoSuchKey", "The key does not exist"); err != nil || len(ms) != 0 {
		t.Errorf("code match: %v %v", ms, err)
	}
	if ms, _ := Error(json.RawMessage(`"NoSuchKey"`), "NoSuchBucket", ""); len(ms) != 1 {
		t.Error("want code mismatch")
	}
	raw := json.RawMessage(`{"code":"InvalidURI","message":"Couldn't parse the specified URI."}`)
	if ms, err := Error(raw, "InvalidURI", "Couldn't parse the specified URI."); err != nil || len(ms) != 0 {
		t.Errorf("code+message: %v %v", ms, err)
	}
	if ms, _ := Error(raw, "InvalidURI", "other"); len(ms) != 1 {
		t.Error("want message mismatch")
	}
}

func TestBody(t *testing.T) {
	body := []byte("hello")
	if ms, err := Body(json.RawMessage(`"hello"`), body, nil); err != nil || len(ms) != 0 {
		t.Errorf("string body: %v %v", ms, err)
	}
	if ms, _ := Body(json.RawMessage(`"world"`), body, nil); len(ms) != 1 {
		t.Error("want body mismatch")
	}
	if ms, err := Body(json.RawMessage(`{"$base64":"aGVsbG8="}`), body, nil); err != nil || len(ms) != 0 {
		t.Errorf("base64 body: %v %v", ms, err)
	}
	resolve := func(name string) ([]byte, error) { return []byte("hello"), nil }
	if ms, err := Body(json.RawMessage(`{"$data":"part1"}`), body, resolve); err != nil || len(ms) != 0 {
		t.Errorf("$data body: %v %v", ms, err)
	}
	// Digest assertion: md5("hello") = 5d41402abc4b2a76b9719d911017c592
	dig := json.RawMessage(`{"$size":5,"$md5":"5d41402abc4b2a76b9719d911017c592"}`)
	if ms, err := Body(dig, body, nil); err != nil || len(ms) != 0 {
		t.Errorf("digest body: %v %v", ms, err)
	}
	if ms, _ := Body(json.RawMessage(`{"$size":6}`), body, nil); len(ms) != 1 {
		t.Error("want size mismatch")
	}
	if ms, _ := Body(json.RawMessage(`{"$sha256":"nope"}`), body, nil); len(ms) != 1 {
		t.Error("want sha256 mismatch")
	}
}

func TestContent(t *testing.T) {
	if b, err := Content(json.RawMessage(`"abc"`), nil); err != nil || string(b) != "abc" {
		t.Errorf("string content: %q %v", b, err)
	}
	if b, err := Content(json.RawMessage(`{"$base64":"AQID"}`), nil); err != nil || len(b) != 3 || b[0] != 1 {
		t.Errorf("base64 content: %v %v", b, err)
	}
	if _, err := Content(json.RawMessage(`{"$data":"x"}`), nil); err == nil {
		t.Error("$data with nil resolver must error")
	}
	if _, err := Content(json.RawMessage(`{"other":1}`), nil); err == nil {
		t.Error("invalid descriptor must error")
	}
}
