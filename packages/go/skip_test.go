package s3tests

import (
	"testing"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// applyRunOptions mirrors Run's initialization so skipReason can be tested in
// isolation: quirks are skipped by default.
func applyRunOptions(opts ...RunOption) runOptions {
	o := runOptions{}
	defaultSkip(&o)
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func TestDefaultQuirkSkip(t *testing.T) {
	quirk := &s3vectors.Vector{ID: "a", Tags: []string{"tier-1", "quirk:not-aws"}}
	dir := &s3vectors.Vector{ID: "b", Tags: []string{"quirk:directory-bucket"}}
	plain := &s3vectors.Vector{ID: "c", Tags: []string{"tier-1", "listing"}}

	skipped := func(o runOptions, v *s3vectors.Vector) bool {
		_, ok := o.skipReason(v)
		return ok
	}

	// Default: quirk vectors are skipped, ordinary vectors are not.
	def := applyRunOptions()
	if !skipped(def, quirk) {
		t.Error("quirk vector should be skipped by default")
	}
	if !skipped(def, dir) {
		t.Error("directory-bucket quirk should be skipped by default")
	}
	if skipped(def, plain) {
		t.Error("non-quirk vector should not be skipped")
	}

	// NoSkip opts an exact tag back in, leaving other quirks skipped.
	no := applyRunOptions(NoSkip("quirk:directory-bucket"))
	if skipped(no, dir) {
		t.Error("NoSkip should run the directory-bucket quirk")
	}
	if !skipped(no, quirk) {
		t.Error("NoSkip of one tag must not un-skip other quirks")
	}

	// NoSkipMatching opts every quirk back in.
	all := applyRunOptions(NoSkipMatching("quirk:*"))
	if skipped(all, quirk) || skipped(all, dir) {
		t.Error("NoSkipMatching(quirk:*) should run all quirks")
	}

	// Explicit Skip still applies (and to non-quirk vectors too).
	ex := applyRunOptions(Skip("known bug", IDs("c")))
	if !skipped(ex, plain) {
		t.Error("explicit Skip should be honored")
	}

	// NoSkip unskips any matching Skip, not just the default quirk one.
	plainTagged := &s3vectors.Vector{ID: "d", Tags: []string{"flaky"}}
	un := applyRunOptions(Skip("flaky on this target", TagsMatching("flaky")), NoSkip("flaky"))
	if skipped(un, plainTagged) {
		t.Error("NoSkip should unskip an explicit Skip too")
	}
}
