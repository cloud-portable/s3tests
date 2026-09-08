package s3tests

import (
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// quirkTagGlob matches vectors whose expectation a general-purpose,
// AWS-tracking target does not reproduce (quirk:not-aws, quirk:directory-bucket,
// quirk:us-east-1-legacy, …). Such vectors contradict the baseline vectors, so
// Run skips them by default via defaultSkip; NoSkip opts them
// back in.
const quirkTagGlob = "quirk:*"

const quirkSkipReason = "quirk vector skipped by default (run it with NoSkip)"

// defaultSkip is applied before any caller option, so Run skips quirk vectors
// unless a later NoSkip unskips them.
func defaultSkip(o *runOptions) { Skip(quirkSkipReason, TagsMatching(quirkTagGlob))(o) }

// RunOption adjusts how Run treats the vectors it is given. Options are
// applied in order; see Skip, SkipFunc and NoSkip.
type RunOption func(*runOptions)

type runOptions struct {
	skips []func(*s3vectors.Vector) (reason string, skip bool)
	// unskips force a vector to run even when a skip rule matches it; any
	// match wins. NoSkip populates them.
	unskips []FilterFunc
}

// skipReason reports whether a vector is skipped, and why. The first matching
// Skip / SkipFunc supplies the reason; a vector is then skipped unless a
// NoSkip unskip matches it.
func (o *runOptions) skipReason(v *s3vectors.Vector) (string, bool) {
	reason, skip := "", false
	for _, s := range o.skips {
		if r, ok := s(v); ok {
			reason, skip = r, true
			break
		}
	}
	if !skip {
		return "", false
	}
	for _, u := range o.unskips {
		if u(v) {
			return "", false
		}
	}
	return reason, true
}

// NoSkip forces vectors matching every given filter (logical AND, exactly as
// Skip selects) to run even when a Skip rule — including the default quirk
// skip — would skip them. Several NoSkip options compose: a vector matched by
// any of them runs. Filters are the same ones Skip and ApplyFilters take, so a
// vector can be un-skipped by tag, id or group. Examples:
//
//	NoSkip(s3tests.TagsMatching("quirk:*")) // run every quirk vector
//	NoSkip(s3tests.IDs("multipart-0013"))   // run one specific vector
func NoSkip(filters ...FilterFunc) RunOption {
	return func(o *runOptions) {
		o.unskips = append(o.unskips, func(v *s3vectors.Vector) bool {
			for _, f := range filters {
				if !f(v) {
					return false
				}
			}
			return true
		})
	}
}

// Skip records vectors matching every given filter (logical AND, exactly as
// ApplyFilters selects) as Skipped with the given reason instead of executing
// them. Unlike dropping vectors with ApplyFilters beforehand, skipped vectors
// still appear in Run's result stream — with Outcome Skipped, the Reason, no
// steps and zero Duration — so reports stay comparable across runs and
// document what was deliberately not exercised:
//
//	runner.Run(ctx, selected,
//		s3tests.Skip("known server bug #123", s3tests.IDs("multipart-0013")),
//		s3tests.Skip("ACLs unsupported", s3tests.Groups("acl", "cors")),
//	)
//
// With no filters every vector is skipped (a dry run that lists the
// selection). Several Skip options compose: the first one matching a vector
// supplies its reason.
func Skip(reason string, filters ...FilterFunc) RunOption {
	return SkipFunc(func(v *s3vectors.Vector) (string, bool) {
		for _, f := range filters {
			if !f(v) {
				return "", false
			}
		}
		return reason, true
	})
}

// SkipFunc is the general form of Skip: skip is consulted for each vector
// before it runs and reports whether to skip it and why. Use it when the
// reason varies per vector, e.g. a skip-list mapping ids to tracking issues:
//
//	known := map[string]string{"multipart-0013": "issue #123"}
//	s3tests.SkipFunc(func(v *s3vectors.Vector) (string, bool) {
//		reason, ok := known[v.ID]
//		return reason, ok
//	})
func SkipFunc(skip func(v *s3vectors.Vector) (reason string, ok bool)) RunOption {
	return func(o *runOptions) { o.skips = append(o.skips, skip) }
}
