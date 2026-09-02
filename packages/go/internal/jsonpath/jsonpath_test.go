package jsonpath

import "testing"

var doc = map[string]any{
	"UploadId": "UP1",
	"ETag":     `"abc"`,
	"Contents": []any{
		map[string]any{"Key": "a", "Size": int64(5)},
		map[string]any{"Key": "b"},
	},
	"CopyPartResult": map[string]any{"ETag": `"xyz"`},
	"headers":        map[string]any{"etag": `"h"`},
	"status":         int64(200),
	"Deep":           []any{[]any{"x"}},
}

func TestGet(t *testing.T) {
	cases := []struct {
		path string
		want any
	}{
		{"UploadId", "UP1"},
		{"Contents[0].Key", "a"},
		{"Contents[1].Key", "b"},
		{"Contents[0].Size", int64(5)},
		{"CopyPartResult.ETag", `"xyz"`},
		{"headers.etag", `"h"`},
		{"status", int64(200)},
		{"Deep[0][0]", "x"},
	}
	for _, c := range cases {
		got, err := Get(doc, c.path)
		if err != nil {
			t.Errorf("Get(%q): %v", c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("Get(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestGetErrors(t *testing.T) {
	for _, p := range []string{
		"", "Nope", "Contents[2].Key", "Contents[0].Nope", "UploadId.Sub",
		"Contents[x]", "Contents[", "Contents]", ".Leading", "Trailing.",
		"Contents[-1]",
	} {
		if _, err := Get(doc, p); err == nil {
			t.Errorf("Get(%q): want error", p)
		}
	}
}

func TestGetString(t *testing.T) {
	if s, err := GetString(doc, "Contents[0].Size"); err != nil || s != "5" {
		t.Errorf("GetString Size = %q, %v", s, err)
	}
	if s, err := GetString(doc, "UploadId"); err != nil || s != "UP1" {
		t.Errorf("GetString UploadId = %q, %v", s, err)
	}
	if _, err := GetString(doc, "Contents"); err == nil {
		t.Error("GetString on array: want error")
	}
}
