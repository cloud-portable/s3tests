package html

import (
	"bytes"
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
			ID: "multipart-0007", Group: "multipart", Title: "bad part etag <script>alert(1)</script>",
			Outcome: s3tests.Fail,
			Steps: []s3tests.StepResult{
				{Index: 0, Name: "CreateMultipartUpload", Passed: true},
				{Index: 2, Name: "CompleteMultipartUpload",
					Err: "transport hiccup",
					Failures: []s3tests.CheckFailure{
						{Field: "status", Expected: "400", Actual: "200"},
						{Field: "error", Expected: `InvalidPart & <script>alert(1)</script>`, Actual: "(no error)"},
					}},
			},
		},
		{
			ID: "lifecycle-config-0010", Group: "lifecycle-config", Outcome: s3tests.Fail,
			RunnerError: "operation PutBucketLifecycle is not supported",
			Steps:       []s3tests.StepResult{{Index: 1, Name: "PutBucketLifecycle"}},
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

func render(t *testing.T, results []s3tests.VectorResult, meta report.Meta) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, slices.Values(results), meta); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestPageStructure(t *testing.T) {
	out := render(t, sampleResults(), report.Meta{
		CorpusVersion: "1.0.0",
		Target:        "MinIO TEST",
		Properties:    map[string]string{"zeta": "z", "alpha": "a"},
	})

	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Error("missing doctype")
	}
	// Self-contained, zero JS.
	if strings.Contains(out, "<script") {
		t.Error("page must contain no JavaScript")
	}
	if strings.Contains(out, "http://") || strings.Contains(out, "https://") {
		t.Error("page must reference no external resources")
	}

	// Summary: 5 attempted (skipped excluded), 2 pass => 40.0%.
	for _, want := range []string{
		"40.0%", ">2/5<", "corpus 1.0.0", "MinIO TEST",
		"alpha: a", "zeta: z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	// Properties sorted: alpha before zeta.
	if strings.Index(out, "alpha: a") > strings.Index(out, "zeta: z") {
		t.Error("properties not sorted")
	}

	// Group rows: multipart 1/2 = 50% => medium; lifecycle-config 0/1 => low;
	// versioning 0/1 attempted => low; object-crud 1/1 => high.
	for _, want := range []string{
		`class="medium"`, `class="low"`, `class="high"`,
		`href="#group-multipart"`, `id="group-multipart"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}

	// Badges: blocked is never styled as fail; runner error is "error".
	for _, want := range []string{
		`<span class="badge pass">pass</span>`,
		`<span class="badge fail">fail</span>`,
		`<span class="badge blocked">blocked</span>`,
		`<span class="badge error">error</span>`,
		`<span class="badge skipped">skipped</span>`,
		"runner error: operation PutBucketLifecycle is not supported",
		"blocked: prerequisite $bucket b1: simulated outage",
		"skipped: excluded by tag filter: slow",
		"warning: teardown x: BucketNotEmpty",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}

	// Failure detail: summary line + every expected/actual line in <details>.
	for _, want := range []string{
		"<details><summary>step 3 (CompleteMultipartUpload): transport hiccup</summary>",
		"status: expected 400, got 200",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

func TestEscaping(t *testing.T) {
	out := render(t, sampleResults(), report.Meta{})
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Fatal("unescaped script content in output")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("escaped title/expected content missing")
	}
	if !strings.Contains(out, "InvalidPart &amp;") {
		t.Error("ampersand not escaped")
	}
}

func TestOmitSkipped(t *testing.T) {
	out := render(t, sampleResults(), report.Meta{OmitSkipped: true})
	if strings.Contains(out, "versioning-0004") {
		t.Error("skipped vector must be omitted")
	}
	if !strings.Contains(out, "versioning-0003") {
		t.Error("blocked vector must be kept")
	}
}

func TestDeterministic(t *testing.T) {
	meta := report.Meta{CorpusVersion: "1.0.0", Properties: map[string]string{"b": "2", "a": "1", "c": "3"}}
	if render(t, sampleResults(), meta) != render(t, sampleResults(), meta) {
		t.Error("output must be byte-for-byte deterministic")
	}
}

func TestEmptyRun(t *testing.T) {
	out := render(t, nil, report.Meta{})
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Error("empty run must still render a page")
	}
	if !strings.Contains(out, "—") {
		t.Error("empty run shows a dash pass rate")
	}
}
