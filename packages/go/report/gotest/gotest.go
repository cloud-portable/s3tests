// Package gotest reports runner results through Go's testing package: one
// t.Run subtest per vector, so `go test` itself renders the outcome — its
// text output, -v verbosity and CI integrations come for free.
package gotest

import (
	"fmt"
	"iter"
	"strings"
	"testing"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
)

// Run reports each vector result as a subtest of t, named by the vector id
// (ids embed the area, e.g. "multipart-0007", and are corpus-unique).
//
//	func TestS3Compat(t *testing.T) {
//		runner, err := s3tests.New(cfg)
//		if err != nil {
//			t.Fatal(err)
//		}
//		vectors, err := s3tests.Vectors()
//		if err != nil {
//			t.Fatal(err)
//		}
//		gotest.Run(t, runner.Run(t.Context(), s3tests.ApplyFilters(vectors, s3tests.Groups("object-crud"))))
//	}
//
// A pass returns (logging title and the vector's real duration), a fail calls
// t.Fatal with the failing step's expected/actual detail, and blocked or
// skipped vectors skip the subtest with a prefixed reason.
//
// Note that `go test -run 'TestS3Compat/multipart-0007'` filters which
// subtests are *reported*, not which vectors *execute* — the runner has
// already produced every result by the time its subtest runs. Select vectors
// before the run instead (s3tests.ApplyFilters).
func Run(t *testing.T, results iter.Seq[s3tests.VectorResult]) {
	for res := range results {
		t.Run(res.ID, func(t *testing.T) {
			report(t, res)
		})
	}
}

// testingT is the subset of *testing.T that report drives, split out so the
// Fatalf path is unit-testable with a recorder.
type testingT interface {
	Helper()
	Logf(format string, args ...any)
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

var _ testingT = (*testing.T)(nil)

// report maps one result onto t. Skipf/Fatalf stop the body, so anything
// worth surfacing (warnings) is logged first.
func report(t testingT, res s3tests.VectorResult) {
	t.Helper()
	for _, w := range res.Warnings {
		t.Logf("warning: %s", w)
	}
	switch res.Outcome {
	case s3tests.Pass:
		// The subtest's own wall time is ~0 (execution already happened in
		// the runner), so log the vector's real duration.
		t.Logf("%s (%.3fs)", res.Title, res.Duration.Seconds())
	case s3tests.Fail:
		if res.RunnerError != "" {
			t.Fatalf("runner error: %s", res.RunnerError)
			return
		}
		t.Fatalf("%s", failureDetail(res))
	case s3tests.Blocked:
		t.Skipf("blocked: %s", res.Reason)
	case s3tests.Skipped:
		t.Skipf("skipped: %s", res.Reason)
	default:
		t.Fatalf("unknown outcome %q", res.Outcome)
	}
}

// failureDetail renders the failing (last executed) step: a "step N (name):"
// header followed by one line per expectation mismatch.
func failureDetail(res s3tests.VectorResult) string {
	if len(res.Steps) == 0 {
		return res.Reason
	}
	step := res.Steps[len(res.Steps)-1]
	var b strings.Builder
	fmt.Fprintf(&b, "step %d (%s):", step.Index+1, step.Name)
	if step.Err != "" {
		fmt.Fprintf(&b, "\n  %s", step.Err)
	}
	for _, f := range step.Failures {
		fmt.Fprintf(&b, "\n  %s: expected %s, got %s", f.Field, f.Expected, f.Actual)
	}
	return b.String()
}
