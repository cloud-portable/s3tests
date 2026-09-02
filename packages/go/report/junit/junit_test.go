package junit

import (
	"bytes"
	"encoding/xml"
	"slices"
	"strings"
	"testing"
	"time"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
	"github.com/cloud-portable/s3tests/packages/go/report"
)

func sampleResults() []s3tests.VectorResult {
	return []s3tests.VectorResult{
		{
			ID: "multipart-0001", Group: "multipart", Title: "two-part upload",
			Tags: []string{"tier-1", "multipart"}, Outcome: s3tests.Pass,
			Duration: 1234 * time.Millisecond,
		},
		{
			ID: "multipart-0007", Group: "multipart", Outcome: s3tests.Fail,
			Steps: []s3tests.StepResult{
				{Index: 0, Name: "CreateMultipartUpload", Passed: true},
				{Index: 2, Name: "CompleteMultipartUpload", Failures: []s3tests.CheckFailure{
					{Field: "status", Expected: "400", Actual: "200"},
					{Field: "error", Expected: `InvalidPart & "<quoted>"`, Actual: "(no error)"},
				}},
			},
		},
		{
			ID: "lifecycle-config-0010", Group: "lifecycle-config", Outcome: s3tests.Fail,
			RunnerError: "operation PutBucketLifecycle is not supported by aws-sdk-go-v2 service/s3",
			Steps: []s3tests.StepResult{
				{Index: 1, Name: "PutBucketLifecycle", Err: "operation PutBucketLifecycle is not supported by aws-sdk-go-v2 service/s3"},
			},
		},
		{
			ID: "versioning-0003", Group: "versioning", Outcome: s3tests.Blocked,
			Reason: "prerequisite $bucket b1: simulated outage",
		},
		{
			ID: "versioning-0004", Group: "versioning", Outcome: s3tests.Skipped,
			Reason: "excluded by tag filter: slow",
		},
		{
			ID: "object-crud-0169", Group: "object-crud", Outcome: s3tests.Pass,
			Warnings: []string{"teardown x: BucketNotEmpty"},
		},
	}
}

// parsed mirrors the JUnit shape for assertions.
type parsed struct {
	Name     string  `xml:"name,attr"`
	Tests    int     `xml:"tests,attr"`
	Failures int     `xml:"failures,attr"`
	Errors   int     `xml:"errors,attr"`
	Skipped  int     `xml:"skipped,attr"`
	Suites   []suite `xml:"testsuite"`
}
type suite struct {
	Name     string     `xml:"name,attr"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Skipped  int        `xml:"skipped,attr"`
	Props    []property `xml:"properties>property"`
	Cases    []testcase `xml:"testcase"`
}
type property struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}
type testcase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	Time      string     `xml:"time,attr"`
	Props     []property `xml:"properties>property"`
	Skipped   *message   `xml:"skipped"`
	Failure   *result    `xml:"failure"`
	Error     *result    `xml:"error"`
	SystemOut string     `xml:"system-out"`
}
type message struct {
	Message string `xml:"message,attr"`
}
type result struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

func render(t *testing.T, results []s3tests.VectorResult, meta report.Meta) (string, parsed) {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, slices.Values(results), meta); err != nil {
		t.Fatal(err)
	}
	var p parsed
	if err := xml.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("output is not valid XML: %v\n%s", err, buf.String())
	}
	return buf.String(), p
}

func find(t *testing.T, p parsed, id string) testcase {
	t.Helper()
	for _, s := range p.Suites {
		for _, c := range s.Cases {
			if c.Name == id {
				return c
			}
		}
	}
	t.Fatalf("testcase %s not found", id)
	return testcase{}
}

func TestJUnitMapping(t *testing.T) {
	raw, p := render(t, sampleResults(), report.Meta{
		CorpusVersion: "1.0.0",
		Target:        "MinIO TEST",
		Properties:    map[string]string{"zeta": "z", "alpha": "a"},
	})

	if p.Name != "s3vectors" || p.Tests != 6 || p.Failures != 1 || p.Errors != 1 || p.Skipped != 2 {
		t.Errorf("root attrs: %+v", p)
	}
	if len(p.Suites) != 4 {
		t.Fatalf("want 4 area suites, got %d", len(p.Suites))
	}
	// Areas in first-encounter order.
	if p.Suites[0].Name != "multipart" || p.Suites[2].Name != "versioning" {
		t.Errorf("suite order: %v", p.Suites)
	}
	mp := p.Suites[0]
	if mp.Tests != 2 || mp.Failures != 1 || mp.Errors != 0 || mp.Skipped != 0 {
		t.Errorf("multipart suite attrs: %+v", mp)
	}
	// Properties: corpusVersion, target, then extras sorted.
	wantProps := []property{
		{"corpusVersion", "1.0.0"}, {"target", "MinIO TEST"}, {"alpha", "a"}, {"zeta", "z"},
	}
	if !slices.Equal(mp.Props, wantProps) {
		t.Errorf("suite properties: %+v", mp.Props)
	}

	pass := find(t, p, "multipart-0001")
	if pass.Failure != nil || pass.Error != nil || pass.Skipped != nil {
		t.Errorf("pass case must have no child: %+v", pass)
	}
	if pass.Classname != "multipart" || pass.Time != "1.234" {
		t.Errorf("pass case attrs: %+v", pass)
	}
	if len(pass.Props) != 1 || pass.Props[0].Name != "tags" || pass.Props[0].Value != "tier-1,multipart" {
		t.Errorf("tags property: %+v", pass.Props)
	}

	fail := find(t, p, "multipart-0007")
	if fail.Failure == nil || fail.Error != nil {
		t.Fatalf("fail case: %+v", fail)
	}
	if fail.Failure.Message != "step 3 (CompleteMultipartUpload): status: expected 400, got 200" {
		t.Errorf("failure message: %q", fail.Failure.Message)
	}
	if !strings.Contains(fail.Failure.Body, `error: expected InvalidPart & "<quoted>", got (no error)`) {
		t.Errorf("failure body: %q", fail.Failure.Body)
	}

	rerr := find(t, p, "lifecycle-config-0010")
	if rerr.Error == nil || rerr.Failure != nil {
		t.Fatalf("runner error must be <error>, not <failure>: %+v", rerr)
	}
	if !strings.HasPrefix(rerr.Error.Message, "runner error: operation PutBucketLifecycle") {
		t.Errorf("error message: %q", rerr.Error.Message)
	}
	lc := p.Suites[1]
	if lc.Name != "lifecycle-config" || lc.Errors != 1 || lc.Failures != 0 {
		t.Errorf("errors counted as errors, not failures: %+v", lc)
	}

	blocked := find(t, p, "versioning-0003")
	if blocked.Failure != nil || blocked.Error != nil {
		t.Fatal("blocked must NEVER be a failure")
	}
	if blocked.Skipped == nil || blocked.Skipped.Message != "blocked: prerequisite $bucket b1: simulated outage" {
		t.Errorf("blocked mapping: %+v", blocked.Skipped)
	}

	skipped := find(t, p, "versioning-0004")
	if skipped.Skipped == nil || skipped.Skipped.Message != "skipped: excluded by tag filter: slow" {
		t.Errorf("skipped mapping: %+v", skipped.Skipped)
	}

	warned := find(t, p, "object-crud-0169")
	if warned.SystemOut != "teardown x: BucketNotEmpty" {
		t.Errorf("warnings must land in system-out: %q", warned.SystemOut)
	}

	if !strings.HasPrefix(raw, xml.Header) {
		t.Error("missing XML declaration")
	}
}

func TestJUnitOmitSkipped(t *testing.T) {
	_, p := render(t, sampleResults(), report.Meta{OmitSkipped: true})
	if p.Tests != 5 || p.Skipped != 1 { // blocked stays; filter-skip dropped
		t.Errorf("OmitSkipped counts: %+v", p)
	}
	for _, s := range p.Suites {
		for _, c := range s.Cases {
			if c.Name == "versioning-0004" {
				t.Error("filter-skipped case must be omitted")
			}
		}
	}
	if _, blockedKept := findOpt(p, "versioning-0003"); !blockedKept {
		t.Error("blocked case must be kept by OmitSkipped")
	}
}

func findOpt(p parsed, id string) (testcase, bool) {
	for _, s := range p.Suites {
		for _, c := range s.Cases {
			if c.Name == id {
				return c, true
			}
		}
	}
	return testcase{}, false
}

func TestJUnitDeterministic(t *testing.T) {
	meta := report.Meta{CorpusVersion: "1.0.0", Properties: map[string]string{"b": "2", "a": "1", "c": "3"}}
	a, _ := render(t, sampleResults(), meta)
	b, _ := render(t, sampleResults(), meta)
	if a != b {
		t.Error("output must be byte-for-byte deterministic")
	}
}

func TestJUnitEmpty(t *testing.T) {
	raw, p := render(t, nil, report.Meta{})
	if p.Tests != 0 || len(p.Suites) != 0 {
		t.Errorf("empty run: %+v", p)
	}
	if !strings.Contains(raw, "<testsuites") {
		t.Errorf("empty run still emits a root element: %s", raw)
	}
}
