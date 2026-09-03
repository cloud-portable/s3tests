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
			Source:   "https://github.com/linux-kdevops/msst-s3/blob/main/tests/test.py#L1",
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
			// The runner records the same message as the step error; the
			// report must not render it twice.
			Steps: []s3tests.StepResult{{Index: 1, Name: "PutBucketLifecycle",
				Err: "operation PutBucketLifecycle is not supported"}},
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
	// Self-contained, zero JS: no scripts and no external resource loads
	// (vector source anchors are navigation, not resources).
	for _, banned := range []string{"<script", "<link", "<img", "@import", "url("} {
		if strings.Contains(out, banned) {
			t.Errorf("page must not contain %q", banned)
		}
	}

	// Header: 5 attempted (skipped excluded), 2 pass => 40.0%; split badge
	// segments carry the counts; fail badge jumps to the first failure.
	for _, want := range []string{
		`<p class="eyebrow">S3 compatibility report</p>`,
		"<h1>MinIO TEST</h1>",
		"40.0% pass (2/5)",
		"corpus 1.0.0",
		`>1 fail</a>`, `>1 blocked</a>`, `>1 errors</a>`, `>1 skipped</a>`, `>2 pass</a>`,
		`href="#v-multipart-0007"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	// Properties sorted in the provenance list.
	if strings.Index(out, "alpha") > strings.Index(out, "zeta") {
		t.Error("properties not sorted")
	}

	// Groups summary table: multipart 1/2 = 50% => medium; lifecycle-config
	// 0/1 => low; object-crud 1/1 => high. Rows tint and percentages color by
	// watermark; bars carry the pass fraction; names link to their section.
	for _, want := range []string{
		`<tr class="medium">`,
		`<tr class="low">`,
		`<tr class="high">`,
		`class="num pct medium">50.0%<`,
		`class="num pct high">100.0%<`,
		`style="width: 50%"`,
		`style="width: 100%"`,
		`<td class="group"><a href="#group-multipart">multipart</a></td>`,
		`class="num zero">0<`,                    // zero problem-counts render dimmed
		`id="group-multipart" open`,              // failing group starts expanded
		`id="group-object-crud">`,                // all-pass group starts collapsed
		`<span class="counts">1/2 passed</span>`, // multipart summary
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}

	// Vector cards: badges per outcome; failing card opens with the
	// expected-vs-actual block; blocked is never styled as fail.
	for _, want := range []string{
		`class="badge badge-pass">pass<`,
		`class="badge badge-fail">fail<`,
		`class="badge badge-blocked">blocked<`,
		`class="badge badge-error">error<`,
		`class="badge badge-skipped">skipped<`,
		"step 3 (CompleteMultipartUpload) failed",
		"transport hiccup",
		"status: expected 400, got 200",
		"runner error: operation PutBucketLifecycle is not supported",
		"blocked: prerequisite $bucket b1: simulated outage",
		"skipped: excluded by tag filter: slow",
		"warning: teardown x: BucketNotEmpty",
		`href="https://github.com/linux-kdevops/msst-s3/blob/main/tests/test.py#L1"`,
		`<span class="tag">tier-1</span><span class="tag">multipart</span>`,
		`class="test-desc"`,    // description block (title + tags)...
		`class="test-outcome"`, // ...visually separated from the outcome block
		"test time 1.2s",       // summed vector durations (header badge + provenance)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	if strings.Contains(out, `class="test-item fail" id="v-versioning-0003"`) {
		t.Error("blocked vector must not be styled as a failure")
	}
	// Vector cards all start collapsed (only group sections auto-expand).
	if strings.Contains(out, "<details open>") {
		t.Error("vector detail cards must default closed")
	}
	// The red summary line must not repeat the mismatch text (it lives only
	// in the detail block), and a runner error whose detail adds nothing
	// renders the message once.
	if strings.Contains(out, "step 3 (CompleteMultipartUpload): transport hiccup") {
		t.Error("summary line must not duplicate the first mismatch")
	}
	if strings.Count(out, "operation PutBucketLifecycle is not supported") != 1 {
		t.Error("runner error message must render exactly once")
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

// Results arrive in completion order (interleaved under concurrency); the
// report presents groups by name and vectors by id regardless.
func TestSortedByID(t *testing.T) {
	shuffled := sampleResults()
	slices.Reverse(shuffled)
	out := render(t, shuffled, report.Meta{})

	inOrder := func(a, b string) {
		t.Helper()
		ia, ib := strings.Index(out, a), strings.Index(out, b)
		if ia < 0 || ib < 0 || ia > ib {
			t.Errorf("%q must precede %q (%d vs %d)", a, b, ia, ib)
		}
	}
	// Groups sorted by name despite reversed arrival.
	inOrder(`id="group-lifecycle-config"`, `id="group-multipart"`)
	inOrder(`id="group-multipart"`, `id="group-object-crud"`)
	inOrder(`id="group-object-crud"`, `id="group-versioning"`)
	// Vectors within a group sorted by id.
	inOrder(`id="v-multipart-0001"`, `id="v-multipart-0007"`)
	inOrder(`id="v-versioning-0003"`, `id="v-versioning-0004"`)
	// The fail-badge jump targets the first plain fail in presentation order
	// (runner errors have their own badge and are excluded from the fail count).
	if !strings.Contains(out, `href="#v-multipart-0007"`) {
		t.Error("fail badge must target the first fail in sorted order")
	}
}

func TestGeneratedAt(t *testing.T) {
	when := time.Date(2026, 9, 3, 14, 5, 6, 0, time.FixedZone("CEST", 2*3600))
	out := render(t, sampleResults(), report.Meta{GeneratedAt: when})
	if !strings.Contains(out, "2026-09-03 12:05:06 UTC") {
		t.Error("GeneratedAt must render in UTC")
	}
	if !strings.Contains(out, ">Generated</span>") {
		t.Error("Generated provenance row missing")
	}
	// Unset => omitted entirely (keeps default output deterministic).
	out = render(t, sampleResults(), report.Meta{})
	if strings.Contains(out, "Generated<") || strings.Contains(out, " at 20") {
		t.Error("zero GeneratedAt must be omitted")
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
	if strings.Contains(out, "skipped</a>") {
		t.Error("skipped badge segment must be absent")
	}
}

func TestDeterministic(t *testing.T) {
	meta := report.Meta{CorpusVersion: "1.0.0", Properties: map[string]string{"b": "2", "a": "1", "c": "3"}}
	first := render(t, sampleResults(), meta)
	second := render(t, sampleResults(), meta)
	if first != second {
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
