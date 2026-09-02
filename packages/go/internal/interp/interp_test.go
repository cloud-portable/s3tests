package interp

import (
	"encoding/json"
	"strings"
	"testing"
)

func testScope() *Scope {
	return &Scope{
		Env: map[string]string{"endpoint": "http://localhost:9000", "region": "us-east-1"},
		Res: map[string]map[string]string{"b1": {"name": "bucket-x"}, "o1": {"etag": `"abc"`, "versionId": "v1"}},
		Cap: map[string]string{"uploadId": "UP123", "etag1": `"e1"`},
		Data: func(name, field string) (string, error) {
			if name == "big" && field == "md5" {
				return "deadbeef", nil
			}
			return "", errNotFound
		},
	}
}

var errNotFound = &notFoundErr{}

type notFoundErr struct{}

func (*notFoundErr) Error() string { return "no such dataset/field" }

func TestString(t *testing.T) {
	sc := testScope()
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"${res.b1.name}", "bucket-x"},
		{"prefix-${res.b1.name}-suffix", "prefix-bucket-x-suffix"},
		{"${cap.uploadId}", "UP123"},
		{"${env.endpoint}", "http://localhost:9000"},
		{"${env.region}", "us-east-1"},
		{"${data.big.md5}", "deadbeef"},
		{"${res.o1.etag}", `"abc"`},           // e.g. a $ne comparison value
		{"$${res.b1.name}", "${res.b1.name}"}, // escaped
		{"cost: $5", "cost: $5"},              // bare $ is literal
		{"a$$b", "a$$b"},                      // $$ not followed by { is literal
		{"${res.b1.name}${cap.etag1}", `bucket-x"e1"`},
	}
	for _, c := range cases {
		got, err := sc.String(c.in)
		if err != nil {
			t.Errorf("String(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("String(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStringErrors(t *testing.T) {
	sc := testScope()
	for _, in := range []string{
		"${res.missing.name}",
		"${res.b1.nope}",
		"${cap.nope}",
		"${env.secret}",
		"${data.nope.md5}",
		"${data.big.nope}",
		"${bogus.x}",
		"${res.b1}",  // missing attr
		"${unclosed", // unterminated
	} {
		if _, err := sc.String(in); err == nil {
			t.Errorf("String(%q): want error, got nil", in)
		}
	}
}

func TestRaw(t *testing.T) {
	sc := testScope()
	in := json.RawMessage(`{"Bucket":"${res.b1.name}","Key":"k","PartNumber":1,"Nested":{"A":["${cap.uploadId}",5,true,null]},"Big":10485760}`)
	out, err := sc.Raw(in)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v["Bucket"] != "bucket-x" {
		t.Errorf("Bucket = %v", v["Bucket"])
	}
	if v["PartNumber"] != json.Number("1") {
		t.Errorf("PartNumber = %v (%T)", v["PartNumber"], v["PartNumber"])
	}
	if v["Big"] != json.Number("10485760") {
		t.Errorf("Big = %v — number must round-trip losslessly", v["Big"])
	}
	nested := v["Nested"].(map[string]any)["A"].([]any)
	if nested[0] != "UP123" {
		t.Errorf("nested capture = %v", nested[0])
	}
	// Input must not be mutated.
	if !strings.Contains(string(in), "${res.b1.name}") {
		t.Error("input RawMessage was mutated")
	}
}

func TestRawUnresolvable(t *testing.T) {
	sc := testScope()
	if _, err := sc.Raw(json.RawMessage(`{"a":"${cap.never}"}`)); err == nil {
		t.Fatal("want error for unresolvable placeholder")
	}
}
