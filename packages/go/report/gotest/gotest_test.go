package gotest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
)

// recorder is a fake testingT capturing calls in order.
type recorder struct {
	logs   []string
	skip   string
	fatal  string
	events []string // "log", "skip", "fatal" in call order
}

func (r *recorder) Helper() {}
func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, sprintf(format, args...))
	r.events = append(r.events, "log")
}
func (r *recorder) Skipf(format string, args ...any) {
	r.skip = sprintf(format, args...)
	r.events = append(r.events, "skip")
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = sprintf(format, args...)
	r.events = append(r.events, "fatal")
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func TestReportPass(t *testing.T) {
	r := &recorder{}
	report(r, s3tests.VectorResult{
		ID: "object-crud-0001", Outcome: s3tests.Pass,
		Title: "put then get", Duration: 1234 * time.Millisecond,
	})
	if r.skip != "" || r.fatal != "" {
		t.Errorf("pass must neither skip nor fail: %+v", r)
	}
	if len(r.logs) != 1 || r.logs[0] != "put then get (1.234s)" {
		t.Errorf("pass log: %v", r.logs)
	}
}

func TestReportFail(t *testing.T) {
	r := &recorder{}
	report(r, s3tests.VectorResult{
		ID: "multipart-0007", Outcome: s3tests.Fail,
		Steps: []s3tests.StepResult{
			{Index: 0, Name: "CreateMultipartUpload", Passed: true},
			{Index: 2, Name: "CompleteMultipartUpload",
				Err: "transport hiccup",
				Failures: []s3tests.CheckFailure{
					{Field: "status", Expected: "400", Actual: "200"},
					{Field: "error", Expected: "InvalidPart", Actual: "(no error)"},
				}},
		},
	})
	want := "step 3 (CompleteMultipartUpload):\n" +
		"  transport hiccup\n" +
		"  status: expected 400, got 200\n" +
		"  error: expected InvalidPart, got (no error)"
	if r.fatal != want {
		t.Errorf("fatal message:\n%q\nwant:\n%q", r.fatal, want)
	}
	if r.skip != "" {
		t.Error("fail must not skip")
	}
}

func TestReportRunnerError(t *testing.T) {
	r := &recorder{}
	report(r, s3tests.VectorResult{
		ID: "lifecycle-config-0010", Outcome: s3tests.Fail,
		RunnerError: "operation PutBucketLifecycle is not supported",
		Steps:       []s3tests.StepResult{{Index: 1, Name: "PutBucketLifecycle"}},
	})
	if r.fatal != "runner error: operation PutBucketLifecycle is not supported" {
		t.Errorf("runner error message: %q", r.fatal)
	}
}

func TestReportBlockedAndSkipped(t *testing.T) {
	r := &recorder{}
	report(r, s3tests.VectorResult{Outcome: s3tests.Blocked, Reason: "prerequisite $bucket b1: down"})
	if r.fatal != "" {
		t.Fatal("blocked must NEVER fail")
	}
	if r.skip != "blocked: prerequisite $bucket b1: down" {
		t.Errorf("blocked skip message: %q", r.skip)
	}

	r = &recorder{}
	report(r, s3tests.VectorResult{Outcome: s3tests.Skipped, Reason: "excluded by tag filter: slow"})
	if r.skip != "skipped: excluded by tag filter: slow" {
		t.Errorf("skipped message: %q", r.skip)
	}
}

// Warnings must be logged before the terminal Skip/Fatal call (which stops a
// real subtest body).
func TestReportWarningsFirst(t *testing.T) {
	r := &recorder{}
	report(r, s3tests.VectorResult{
		Outcome:  s3tests.Fail,
		Warnings: []string{"teardown x: BucketNotEmpty"},
		Steps:    []s3tests.StepResult{{Index: 0, Name: "PutObject", Err: "boom"}},
	})
	if len(r.events) != 2 || r.events[0] != "log" || r.events[1] != "fatal" {
		t.Errorf("event order: %v", r.events)
	}
	if r.logs[0] != "warning: teardown x: BucketNotEmpty" {
		t.Errorf("warning log: %v", r.logs)
	}
}

// Run against a real *testing.T: pass/blocked/skipped results execute as
// subtests named by vector id without failing the suite. (The Fatal path is
// covered by the recorder tests — a real one would fail this run.) Under -v
// this test also demonstrates the rendered output.
func TestRunReal(t *testing.T) {
	results := []s3tests.VectorResult{
		{ID: "object-crud-0001", Group: "object-crud", Title: "put then get",
			Outcome: s3tests.Pass, Duration: 42 * time.Millisecond},
		{ID: "versioning-0003", Group: "versioning", Outcome: s3tests.Blocked,
			Reason: "prerequisite $credential alt: no ProvisionCredential configured"},
		{ID: "versioning-0004", Group: "versioning", Outcome: s3tests.Skipped,
			Reason: "excluded by id filter"},
	}
	Run(t, slices.Values(results))
}
