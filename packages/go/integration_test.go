//go:build integration

package s3tests_test

// Integration test against a real S3 implementation (MinIO by default; see
// the Makefile's `integration` target). It asserts runner *mechanics* — no
// runner errors, sane outcome accounting, clean teardown — not the target's
// compatibility score: pass rates are a statement about the server, and
// failing vectors here may simply be true incompatibilities.

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	s3tests "github.com/alanshaw/s3tests/packages/go"
	"github.com/alanshaw/s3tests/packages/go/report"
	htmlreport "github.com/alanshaw/s3tests/packages/go/report/html"
	"github.com/alanshaw/s3tests/packages/go/report/junit"
)

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Vectors whose failure is a known runner limitation, not a target problem.
var allowedRunnerErrors = map[string]bool{
	"lifecycle-config-0010": true, // PutBucketLifecycle was dropped from aws-sdk-go-v2
}

func TestIntegration(t *testing.T) {
	endpoint := envOr("S3TESTS_ENDPOINT", "http://127.0.0.1:9000")
	creds := credentials.NewStaticCredentialsProvider(
		envOr("S3TESTS_ACCESS_KEY", "minioadmin"),
		envOr("S3TESTS_SECRET_KEY", "minioadmin"), "")

	runner, err := s3tests.New(s3tests.Config{
		Endpoint:    endpoint,
		Credentials: creds,
		Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	groups := strings.Split(envOr("S3TESTS_GROUPS",
		"object-crud,multipart,presigned,anon-access,checksums,wire-headers,cors"), ",")
	vectors, err := s3tests.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	selected := s3tests.ApplyFilters(vectors, s3tests.Groups(groups...))
	if len(selected) == 0 {
		t.Fatalf("group filter %v selected nothing", groups)
	}
	counts := map[s3tests.Outcome]int{}
	var collected []s3tests.VectorResult
	for v := range runner.Run(ctx, selected) {
		counts[v.Outcome]++
		collected = append(collected, v)
		if v.RunnerError != "" && !allowedRunnerErrors[v.ID] {
			t.Errorf("%s: unexpected runner error: %s", v.ID, v.RunnerError)
		}
		for _, w := range v.Warnings {
			t.Logf("%s: warning: %s", v.ID, w)
		}
		// No credential provisioner is configured, so $credential vectors
		// must be blocked, and nothing else should be.
		if v.Outcome == s3tests.Blocked && !strings.Contains(v.Reason, "$credential") &&
			!strings.Contains(v.Reason, "ProvisionCredential") {
			t.Errorf("%s: unexpected block: %s", v.ID, v.Reason)
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("run did not complete: %v", err)
	}
	total := len(collected)
	if total != len(selected) {
		t.Errorf("run yielded %d results for %d selected vectors", total, len(selected))
	}

	t.Logf("corpus %s against %s: pass=%d fail=%d blocked=%d skipped=%d",
		runner.CorpusVersion(), endpoint,
		counts[s3tests.Pass], counts[s3tests.Fail], counts[s3tests.Blocked], counts[s3tests.Skipped])
	if counts[s3tests.Pass]+counts[s3tests.Fail]+counts[s3tests.Blocked]+counts[s3tests.Skipped] != total {
		t.Errorf("outcome counts do not sum: %v != %d", counts, total)
	}
	if counts[s3tests.Pass] == 0 {
		t.Error("no vector passed — endpoint misconfigured?")
	}

	// The JUnit formatter must round-trip the real results: valid XML whose
	// counts match the tally (errors = RunnerError fails; skipped includes
	// blocked per the reporting-guide mapping).
	var buf bytes.Buffer
	if err := junit.Write(&buf, slices.Values(collected), report.Meta{
		CorpusVersion: runner.CorpusVersion(),
		Target:        endpoint,
	}); err != nil {
		t.Fatalf("junit.Write: %v", err)
	}
	var doc struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Skipped  int `xml:"skipped,attr"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("JUnit output is not valid XML: %v", err)
	}
	runnerErrs := 0
	for _, v := range collected {
		if v.Outcome == s3tests.Fail && v.RunnerError != "" {
			runnerErrs++
		}
	}
	if doc.Tests != total || doc.Errors != runnerErrs ||
		doc.Failures != counts[s3tests.Fail]-runnerErrs ||
		doc.Skipped != counts[s3tests.Skipped]+counts[s3tests.Blocked] {
		t.Errorf("JUnit counts %+v don't match tally %v (runner errors %d)", doc, counts, runnerErrs)
	}

	// The HTML reporter must render the real results without error into a
	// non-trivial, self-contained page.
	var htmlBuf bytes.Buffer
	if err := htmlreport.Write(&htmlBuf, slices.Values(collected), report.Meta{
		CorpusVersion: runner.CorpusVersion(),
		Target:        endpoint,
	}); err != nil {
		t.Fatalf("html.Write: %v", err)
	}
	htmlOut := htmlBuf.String()
	if len(htmlOut) < 10_000 || !strings.Contains(htmlOut, runner.CorpusVersion()) ||
		!strings.Contains(htmlOut, `id="group-multipart"`) {
		t.Errorf("html report looks wrong (%d bytes)", len(htmlOut))
	}
	if strings.Contains(htmlOut, "<script") {
		t.Error("html report must contain no JavaScript")
	}

	// Teardown audit: no runner buckets may survive (the curated areas
	// contain no COMPLIANCE-retention vectors). "s3tests-" is the documented
	// default Config.BucketPrefix.
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       "us-east-1",
		UsePathStyle: true,
		Credentials:  creds,
	})
	buckets, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("bucket audit: %v", err)
	}
	for _, b := range buckets.Buckets {
		if strings.HasPrefix(aws.ToString(b.Name), "s3tests-") {
			t.Errorf("teardown leaked bucket %s", aws.ToString(b.Name))
		}
	}
}
