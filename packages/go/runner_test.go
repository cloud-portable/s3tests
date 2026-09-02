package s3tests

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// fakeS3 is a minimal in-memory S3 for exercising runner mechanics: bucket
// create/delete, object put/get, empty version/upload listings, no locking.
type fakeS3 struct {
	mu      sync.Mutex
	buckets map[string]map[string][]byte
}

func newFakeS3() *fakeS3 { return &fakeS3{buckets: map[string]map[string][]byte{}} }

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	bucket := parts[0]
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	q := r.URL.Query()
	writeErr := func(status int, code string) {
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			fmt.Fprintf(w, `<?xml version="1.0"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
		}
	}
	objects, bucketExists := f.buckets[bucket]

	switch {
	case key == "" && r.Method == http.MethodPut: // CreateBucket
		f.buckets[bucket] = map[string][]byte{}
		w.WriteHeader(200)
	case key == "" && r.Method == http.MethodDelete:
		delete(f.buckets, bucket)
		w.WriteHeader(204)
	case key == "" && q.Has("uploads"):
		w.WriteHeader(200)
		io.WriteString(w, `<?xml version="1.0"?><ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case key == "" && q.Has("versions"):
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><ListVersionsResult><IsTruncated>false</IsTruncated>`)
		for k := range objects {
			fmt.Fprintf(&b, "<Version><Key>%s</Key><VersionId>null</VersionId></Version>", k)
		}
		b.WriteString(`</ListVersionsResult>`)
		w.WriteHeader(200)
		io.WriteString(w, b.String())
	case key == "" && q.Has("object-lock"):
		writeErr(404, "ObjectLockConfigurationNotFoundError")
	case key == "" && q.Has("delete") && r.Method == http.MethodPost: // DeleteObjects
		body, _ := io.ReadAll(r.Body)
		var del struct {
			Objects []struct {
				Key string `xml:"Key"`
			} `xml:"Object"`
		}
		xml.Unmarshal(body, &del)
		for _, o := range del.Objects {
			delete(objects, o.Key)
		}
		w.WriteHeader(200)
		io.WriteString(w, `<?xml version="1.0"?><DeleteResult></DeleteResult>`)
	case key != "" && r.Method == http.MethodPut:
		if !bucketExists {
			writeErr(404, "NoSuchBucket")
			return
		}
		body, _ := io.ReadAll(r.Body)
		objects[key] = body
		sum := md5.Sum(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		w.WriteHeader(200)
	case key != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		body, ok := objects[key]
		if !ok {
			writeErr(404, "NoSuchKey")
			return
		}
		sum := md5.Sum(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(200)
		if r.Method == http.MethodGet {
			w.Write(body)
		}
	default:
		writeErr(400, "NotImplemented")
	}
}

func testRunner(t *testing.T, srvURL string) *Runner {
	t.Helper()
	r, err := New(Config{
		Endpoint:    srvURL,
		Credentials: credentials.NewStaticCredentialsProvider("AK", "SK", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func passVector() *s3vectors.Vector {
	return &s3vectors.Vector{
		ID: "test-0001", Group: "test", Kind: "api", Title: "get seeded object", Tags: []string{"tier-1", "test"},
		Prerequisites: []s3vectors.Prerequisite{
			{Bucket: &s3vectors.BucketPrerequisite{Handle: "b1"}},
			{Object: &s3vectors.ObjectPrerequisite{Handle: "o1", Bucket: "b1", Key: "hello.txt", Body: raw(`"hello world"`)}},
		},
		Data: map[string]s3vectors.DataSpec{
			"pat": {Pattern: &s3vectors.PatternData{Pattern: strptr("hello world"), Size: 11}},
		},
		Steps: []s3vectors.Step{
			{Operation: &s3vectors.OperationStep{
				Name:    "GetObject",
				Params:  map[string]json.RawMessage{"Bucket": raw(`"${res.b1.name}"`), "Key": raw(`"${res.o1.key}"`)},
				Capture: map[string]string{"etag": "ETag"},
				Expect: &s3vectors.Expect{
					Status:   200,
					Response: raw(`{"ContentLength": 11, "ETag": "${data.pat.etag}"}`),
					Headers:  map[string]json.RawMessage{"etag": raw(`{"$exists": true}`)},
					Body:     raw(`{"$data": "pat"}`),
				},
			}},
			{Operation: &s3vectors.OperationStep{
				Name:   "HeadObject",
				Params: map[string]json.RawMessage{"Bucket": raw(`"${res.b1.name}"`), "Key": raw(`"${res.o1.key}"`)},
				Expect: &s3vectors.Expect{Response: raw(`{"ETag": "${cap.etag}"}`)},
			}},
			{Operation: &s3vectors.OperationStep{
				Name:   "GetObject",
				Params: map[string]json.RawMessage{"Bucket": raw(`"${res.b1.name}"`), "Key": raw(`"missing.bin"`)},
				Expect: &s3vectors.Expect{Status: 404, Error: raw(`"NoSuchKey"`)},
			}},
		},
	}
}

func strptr(s string) *string { return &s }

func TestRunVectorPass(t *testing.T) {
	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r := testRunner(t, srv.URL)

	res := r.runVector(context.Background(), passVector())
	if res.Outcome != Pass {
		t.Fatalf("outcome = %s, reason=%q runnerErr=%q steps=%+v", res.Outcome, res.Reason, res.RunnerError, res.Steps)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("steps = %d", len(res.Steps))
	}
	if res.Steps[0].Captured["etag"] == "" {
		t.Error("etag capture missing")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings: %v", res.Warnings)
	}
	// Teardown must have removed the bucket.
	fake.mu.Lock()
	if len(fake.buckets) != 0 {
		t.Errorf("teardown left buckets: %v", fake.buckets)
	}
	fake.mu.Unlock()
}

func TestRunVectorFail(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)

	v := passVector()
	v.Steps[0].Operation.Expect.Response = raw(`{"ContentLength": 999}`)
	res := r.runVector(context.Background(), v)
	if res.Outcome != Fail {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if res.RunnerError != "" {
		t.Errorf("compat failure must not set RunnerError: %q", res.RunnerError)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("failing step must abort the vector; ran %d steps", len(res.Steps))
	}
	fs := res.Steps[0]
	if len(fs.Failures) != 1 || fs.Failures[0].Field != "response.ContentLength" {
		t.Errorf("failures = %+v", fs.Failures)
	}
}

func TestRunVectorBlocked(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)
	r.cfg.Provisioner = failingProvisioner{}

	res := r.runVector(context.Background(), passVector())
	if res.Outcome != Blocked {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if !strings.Contains(res.Reason, "prerequisite $bucket b1") {
		t.Errorf("reason = %q", res.Reason)
	}
	if len(res.Steps) != 0 {
		t.Error("blocked vector must not run steps")
	}
}

type failingProvisioner struct{ DefaultProvisioner }

func (failingProvisioner) Bucket(ctx context.Context, t Target, p *s3vectors.BucketPrerequisite, name string) (BucketInfo, error) {
	return BucketInfo{}, fmt.Errorf("simulated provisioning outage")
}

func TestRunVectorCredentialBlocked(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)

	v := &s3vectors.Vector{
		ID: "test-0002", Kind: "api", Title: "needs alt", Tags: []string{"tier-1"},
		Prerequisites: []s3vectors.Prerequisite{{Credential: &s3vectors.CredentialPrerequisite{Handle: "alt"}}},
		Steps: []s3vectors.Step{{Operation: &s3vectors.OperationStep{
			Name: "ListBuckets", Identity: "alt",
		}}},
	}
	res := r.runVector(context.Background(), v)
	if res.Outcome != Blocked {
		t.Fatalf("outcome = %s (no ProvisionCredential => blocked), reason %q", res.Outcome, res.Reason)
	}
}

func TestRunVectorRunnerError(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)

	v := passVector()
	v.Steps[0].Operation.Params["Key"] = raw(`"${cap.neverCaptured}"`)
	res := r.runVector(context.Background(), v)
	if res.Outcome != Fail {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if !strings.Contains(res.RunnerError, "unresolvable placeholder") {
		t.Errorf("RunnerError = %q", res.RunnerError)
	}
}

func corpusVectors(t *testing.T) []*s3vectors.Vector {
	t.Helper()
	vectors, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	return vectors
}

func TestVectors(t *testing.T) {
	vectors := corpusVectors(t)
	if len(vectors) < 1000 {
		t.Errorf("only %d vectors — corpus load broken?", len(vectors))
	}
	for _, v := range vectors {
		if v.Group == "signing" || !v.IsAPI() {
			t.Fatalf("%s: signing vectors must not be loaded", v.ID)
		}
		if v.Group == "" || v.ID == "" {
			t.Fatalf("vector missing group/id: %+v", v)
		}
	}
}

func TestApplyFilters(t *testing.T) {
	vectors := corpusVectors(t)

	if got := ApplyFilters(vectors); len(got) != len(vectors) {
		t.Errorf("no filters must select everything: %d vs %d", len(got), len(vectors))
	}
	one := ApplyFilters(vectors, IDs("object-crud-0001"))
	if len(one) != 1 || one[0].ID != "object-crud-0001" {
		t.Fatalf("IDs filter: %v", one)
	}
	errGroup := ApplyFilters(vectors, Groups("errors"))
	if len(errGroup) == 0 {
		t.Fatal("Groups filter selected nothing")
	}
	for _, v := range errGroup {
		if v.Group != "errors" {
			t.Fatalf("Groups filter leaked %s", v.ID)
		}
	}
	// Filters AND together; excludes drop matches.
	tier1errors := ApplyFilters(vectors, Groups("errors"), Tags("tier-1"))
	if len(tier1errors) == 0 || len(tier1errors) > len(errGroup) {
		t.Errorf("AND semantics: %d of %d", len(tier1errors), len(errGroup))
	}
	skipListed := ApplyFilters(errGroup, ExcludeIDs(errGroup[0].ID))
	if len(skipListed) != len(errGroup)-1 {
		t.Errorf("ExcludeIDs: %d, want %d", len(skipListed), len(errGroup)-1)
	}
	if got := ApplyFilters(errGroup, ExcludeGroups("errors")); len(got) != 0 {
		t.Errorf("ExcludeGroups left %d", len(got))
	}
	if got := ApplyFilters(tier1errors, ExcludeTags("tier-1")); len(got) != 0 {
		t.Errorf("ExcludeTags left %d", len(got))
	}
	// Custom filters are plain functions.
	custom := ApplyFilters(vectors, func(v *s3vectors.Vector) bool { return len(v.Steps) > 10 })
	for _, v := range custom {
		if len(v.Steps) <= 10 {
			t.Fatalf("custom filter leaked %s", v.ID)
		}
	}
	// Order is preserved.
	for i := 1; i < len(errGroup); i++ {
		if errGroup[i-1].ID >= errGroup[i].ID {
			t.Fatalf("order not preserved: %s >= %s", errGroup[i-1].ID, errGroup[i].ID)
		}
	}
}

func TestRunSelectedVectors(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)
	r.cfg.Concurrency = 4

	if r.CorpusVersion() == "" {
		t.Error("missing corpus version")
	}
	selected := ApplyFilters(corpusVectors(t), IDs("object-crud-0001"))
	total := 0
	for res := range r.Run(context.Background(), selected) {
		total++
		if res.ID != "object-crud-0001" {
			t.Errorf("unexpected vector %s", res.ID)
		}
		if res.Outcome == Skipped {
			t.Error("Run must not synthesize skipped results")
		}
	}
	if total != 1 {
		t.Errorf("want exactly 1 result, got %d", total)
	}
}

// Breaking out of the range loop must cancel the run: the iterator returns
// promptly and no further results are delivered.
func TestRunEarlyBreak(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)
	r.cfg.Concurrency = 2

	// The errors area is small (8 vectors) but multi-step; break on the
	// first result while others are in flight.
	selected := ApplyFilters(corpusVectors(t), Groups("errors"))
	done := make(chan int)
	go func() {
		seen := 0
		for range r.Run(context.Background(), selected) {
			seen++
			break
		}
		done <- seen
	}()
	select {
	case seen := <-done:
		if seen != 1 {
			t.Errorf("expected to break after 1 result, saw %d", seen)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("iterator did not return after break — cancel/drain is stuck")
	}
}

func TestRunContextCancelled(t *testing.T) {
	srv := httptest.NewServer(newFakeS3())
	defer srv.Close()
	r := testRunner(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	total := 0
	for range r.Run(ctx, corpusVectors(t)) {
		total++
	}
	if total != 0 {
		t.Errorf("pre-cancelled run must yield nothing, got %d results", total)
	}
}
