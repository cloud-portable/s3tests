package vdata

import (
	"testing"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
	"github.com/cloud-portable/s3vectors/packages/go/datagen"
)

func strp(s string) *string { return &s }

func specs() map[string]s3vectors.DataSpec {
	return map[string]s3vectors.DataSpec{
		"big":   {Prng: &s3vectors.PrngData{Seed: "test-0001/big", Size: 100000}},
		"part1": {Slice: &s3vectors.SliceData{Of: "big", Offset: 0, Length: 50000}},
		"aaa":   {Pattern: &s3vectors.PatternData{Pattern: strp("A"), Size: 16}},
	}
}

// Every derived field must match the reference datagen implementation.
func TestDerivedMatchesDatagen(t *testing.T) {
	sp := specs()
	c := New(sp)
	for _, name := range []string{"big", "part1", "aaa"} {
		for _, field := range datagen.DerivedFields {
			want, err := datagen.Derived(sp, name, field)
			if err != nil {
				t.Fatalf("datagen.Derived(%s,%s): %v", name, field, err)
			}
			got, err := c.Derived(name, field)
			if err != nil {
				t.Fatalf("Cache.Derived(%s,%s): %v", name, field, err)
			}
			if got != want {
				t.Errorf("Derived(%s,%s) = %q, want %q", name, field, got, want)
			}
		}
	}
}

func TestBytesMemoized(t *testing.T) {
	c := New(specs())
	a, err := c.Bytes("big")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.Bytes("big")
	if &a[0] != &b[0] {
		t.Error("Bytes not memoized")
	}
}

func TestErrors(t *testing.T) {
	c := New(specs())
	if _, err := c.Bytes("nope"); err == nil {
		t.Error("want error for unknown dataset")
	}
	if _, err := c.Derived("big", "nope"); err == nil {
		t.Error("want error for unknown field")
	}
}
