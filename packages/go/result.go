package s3tests

import "time"

// Outcome is one of the four vector outcomes defined by the corpus spec.
type Outcome string

const (
	// Pass: all prerequisites established, all steps met their expectations.
	Pass Outcome = "pass"
	// Fail: a step violated its expectations (or the runner could not
	// execute the vector — see VectorResult.RunnerError).
	Fail Outcome = "fail"
	// Blocked: a prerequisite could not be established; the vector was not
	// runnable. Never conflate with Fail.
	Blocked Outcome = "blocked"
	// Skipped: excluded from the run by a filter. Selection happens before
	// Run (see Vectors/ApplyFilters), so the runner itself never produces
	// this outcome — it exists for consumers that synthesize results for
	// vectors they chose not to execute, keeping reports comparable across
	// differently-filtered runs (the reporters render it).
	Skipped Outcome = "skipped"
)

// VectorResult is the outcome of one vector.
type VectorResult struct {
	ID    string
	Group string
	Title string
	Tags  []string
	// Source is the URL of the test this vector was converted from, when the
	// corpus records one — useful in reports for tracing a failure back to
	// its origin.
	Source string

	Outcome Outcome
	// Reason explains Blocked ("prerequisite $bucket b1: ...") and, for
	// consumer-synthesized results, Skipped outcomes.
	Reason string
	// RunnerError is set when a Fail is a runner or vector-definition error
	// (unsupported operation, unresolvable placeholder) rather than a
	// compatibility failure of the server under test.
	RunnerError string

	// Steps holds results for executed steps only (execution stops at the
	// first failing step).
	Steps []StepResult
	// Warnings records non-fatal problems, e.g. teardown leftovers.
	Warnings []string
	Duration time.Duration
}

// StepResult is the observed outcome of one executed step.
type StepResult struct {
	Index     int    // 0-based position in the vector's steps
	Kind      string // "operation" or "http"
	Name      string // operation name, or "METHOD /path" for $http steps
	Presigned bool
	Identity  string // "main", "anonymous", "invalid" or a credential handle

	Status   int // raw HTTP status observed (0 if the request never completed)
	Passed   bool
	Failures []CheckFailure    // expectation mismatches
	Err      string            // transport/dispatch/runner error, if any
	Captured map[string]string // values captured for later steps
	Duration time.Duration
}

// CheckFailure is one expectation mismatch, expressed as expected vs actual.
type CheckFailure struct {
	Field    string // e.g. "status", "error", "response.ETag", "headers.content-range", "body"
	Expected string
	Actual   string
}
