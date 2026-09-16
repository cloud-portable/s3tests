package s3tests

import (
	"strings"
	"testing"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// applyRunOptions mirrors Run's initialization so skipReason can be tested in
// isolation: quirk and large vectors are skipped by default.
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
	no := applyRunOptions(NoSkip(TagsMatching("quirk:directory-bucket")))
	if skipped(no, dir) {
		t.Error("NoSkip should run the directory-bucket quirk")
	}
	if !skipped(no, quirk) {
		t.Error("NoSkip of one tag must not un-skip other quirks")
	}

	// NoSkip with a glob filter opts every quirk back in.
	all := applyRunOptions(NoSkip(TagsMatching("quirk:*")))
	if skipped(all, quirk) || skipped(all, dir) {
		t.Error("NoSkip(TagsMatching(quirk:*)) should run all quirks")
	}

	// Explicit Skip still applies (and to non-quirk vectors too).
	ex := applyRunOptions(Skip("known bug", IDs("c")))
	if !skipped(ex, plain) {
		t.Error("explicit Skip should be honored")
	}

	// NoSkip unskips any matching Skip, not just the default quirk one.
	plainTagged := &s3vectors.Vector{ID: "d", Tags: []string{"flaky"}}
	un := applyRunOptions(Skip("flaky on this target", TagsMatching("flaky")), NoSkip(TagsMatching("flaky")))
	if skipped(un, plainTagged) {
		t.Error("NoSkip should unskip an explicit Skip too")
	}

	// NoSkip accepts any filter, so a single quirk vector can be run by id
	// while the rest stay skipped.
	byID := applyRunOptions(NoSkip(IDs("a")))
	if skipped(byID, quirk) {
		t.Error("NoSkip(IDs) should run the named quirk vector")
	}
	if !skipped(byID, dir) {
		t.Error("NoSkip(IDs) must not un-skip other quirks")
	}
}

func TestDefaultLargeSkip(t *testing.T) {
	big := &s3vectors.Vector{ID: "a", Tags: []string{"tier-1", "copy", "large"}}
	plain := &s3vectors.Vector{ID: "b", Tags: []string{"tier-1", "copy"}}
	both := &s3vectors.Vector{ID: "c", Tags: []string{"large", "quirk:not-aws"}}

	skipped := func(o runOptions, v *s3vectors.Vector) bool {
		_, ok := o.skipReason(v)
		return ok
	}

	def := applyRunOptions()
	if !skipped(def, big) {
		t.Error("large vector should be skipped by default")
	}
	if skipped(def, plain) {
		t.Error("non-large vector should not be skipped")
	}
	if reason, _ := def.skipReason(big); !strings.Contains(reason, "generates gigabytes") {
		t.Errorf("large skip reason = %q", reason)
	}

	// NoSkip opts it back in, by tag or by id.
	if skipped(applyRunOptions(NoSkip(Tags(largeTag))), big) {
		t.Error("NoSkip(Tags(large)) should run the large vector")
	}
	if skipped(applyRunOptions(NoSkip(IDs("a"))), big) {
		t.Error("NoSkip(IDs) should run the large vector")
	}

	// NoSkip un-skips a matching vector wholesale, not rule by rule: a vector
	// that is both quirk and large runs once any NoSkip filter matches it.
	if !skipped(applyRunOptions(), both) {
		t.Error("quirk+large vector should be skipped by default")
	}
	if skipped(applyRunOptions(NoSkip(Tags(largeTag))), both) {
		t.Error("one matching NoSkip should run the quirk+large vector")
	}
	if skipped(applyRunOptions(NoSkip(TagsMatching("quirk:*"))), both) {
		t.Error("either NoSkip filter should be enough")
	}
}
