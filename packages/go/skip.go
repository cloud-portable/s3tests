package s3tests

import (
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// RunOption adjusts how Run treats the vectors it is given. Options are
// applied in order; see Skip and SkipFunc.
type RunOption func(*runOptions)

type runOptions struct {
	skips []func(*s3vectors.Vector) (reason string, skip bool)
}

// skipReason reports whether a vector is skipped by any option, and why.
// The first matching option wins.
func (o *runOptions) skipReason(v *s3vectors.Vector) (string, bool) {
	for _, s := range o.skips {
		if reason, ok := s(v); ok {
			return reason, true
		}
	}
	return "", false
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
