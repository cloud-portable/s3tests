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
	// Skipped: deliberately not executed. Run produces it for vectors
	// matched by a Skip / SkipFunc option (Reason carries the option's
	// reason); consumers may also synthesize it for vectors they filtered
	// out before Run, keeping reports comparable across differently-filtered
	// runs (the reporters render it).
	Skipped Outcome = "skipped"
)

// VectorResult is the outcome of one vector.
type VectorResult struct {
	ID    string   `json:"id"`
	Group string   `json:"group"`
	Title string   `json:"title,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	// Source is the URL of the test this vector was converted from, when the
	// corpus records one — useful in reports for tracing a failure back to
	// its origin.
	Source string `json:"source,omitempty"`

	Outcome Outcome `json:"outcome"`
	// Reason explains Blocked ("prerequisite $bucket b1: ...") and Skipped
	// (the Skip option's reason) outcomes.
	Reason string `json:"reason,omitempty"`
	// RunnerError is set when a Fail is a runner or vector-definition error
	// (unsupported operation, unresolvable placeholder) rather than a
	// compatibility failure of the server under test.
	RunnerError string `json:"runnerError,omitempty"`

	// Steps holds results for executed steps only (execution stops at the
	// first failing step).
	Steps []StepResult `json:"steps,omitempty"`
	// Warnings records non-fatal problems, e.g. teardown leftovers.
	Warnings []string `json:"warnings,omitempty"`
	// Duration JSON-encodes as integer nanoseconds (Go's native
	// time.Duration representation) — the cross-implementation contract.
	Duration time.Duration `json:"duration"`
}

// StepResult is the observed outcome of one executed step.
type StepResult struct {
	Index     int    `json:"index"` // 0-based position in the vector's steps
	Kind      string `json:"kind"`  // "operation" or "http"
	Name      string `json:"name"`  // operation name, or "METHOD /path" for $http steps
	Presigned bool   `json:"presigned,omitempty"`
	Identity  string `json:"identity"` // "main", "anonymous", "invalid" or a credential handle

	Status   int               `json:"status"` // raw HTTP status observed (0 if the request never completed)
	Passed   bool              `json:"passed"`
	Failures []CheckFailure    `json:"failures,omitempty"` // expectation mismatches
	Err      string            `json:"err,omitempty"`      // transport/dispatch/runner error, if any
	Captured map[string]string `json:"captured,omitempty"` // values captured for later steps
	Duration time.Duration     `json:"duration"`
}

// CheckFailure is one expectation mismatch, expressed as expected vs actual.
type CheckFailure struct {
	Field    string `json:"field"` // e.g. "status", "error", "response.ETag", "headers.content-range", "body"
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}
