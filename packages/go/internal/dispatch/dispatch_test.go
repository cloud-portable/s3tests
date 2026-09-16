package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
	"github.com/cloud-portable/s3vectors/packages/go/datagen"
)

func TestSupported(t *testing.T) {
	for _, op := range []string{"GetObject", "PutObject", "CreateMultipartUpload", "ListObjectsV2", "DeleteBucket"} {
		if !Supported(op) {
			t.Errorf("Supported(%s) = false", op)
		}
	}
	if Supported("PutBucketLifecycle") {
		t.Error("PutBucketLifecycle should be unsupported (dropped from aws-sdk-go-v2)")
	}
}

func TestBuildInput(t *testing.T) {
	params := map[string]json.RawMessage{
		"Bucket":     json.RawMessage(`"b"`),
		"Key":        json.RawMessage(`"k"`),
		"Body":       json.RawMessage(`{"$data":"part1"}`),
		"ACL":        json.RawMessage(`"public-read"`),
		"Metadata":   json.RawMessage(`{"a":"1"}`),
		"Expires":    json.RawMessage(`"Sun, 01 Jan 2034 00:00:00 GMT"`),
		"ContentMD5": json.RawMessage(`"md5here"`),
	}
	resolve := func(name string) ([]byte, error) { return []byte("DATA"), nil }
	in, body, err := BuildInput("PutObject", params, Resolver{Bytes: resolve})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "DATA" {
		t.Errorf("body = %q", body)
	}
	poi := in.(*s3.PutObjectInput)
	if *poi.Bucket != "b" || *poi.Key != "k" || *poi.ContentMD5 != "md5here" {
		t.Errorf("basic fields wrong: %+v", poi)
	}
	if poi.ACL != s3types.ObjectCannedACLPublicRead {
		t.Errorf("enum ACL = %q", poi.ACL)
	}
	if poi.Metadata["a"] != "1" {
		t.Errorf("metadata = %v", poi.Metadata)
	}
	if poi.Expires == nil || poi.Expires.Year() != 2034 {
		t.Errorf("Expires = %v", poi.Expires)
	}
	if poi.Body == nil {
		t.Error("Body not set")
	}
}

func TestBuildInputNested(t *testing.T) {
	params := map[string]json.RawMessage{
		"Bucket": json.RawMessage(`"b"`),
		"Key":    json.RawMessage(`"k"`),
		"MultipartUpload": json.RawMessage(`{"Parts":[
			{"PartNumber":1,"ETag":"\"e1\""},
			{"PartNumber":2,"ETag":"\"e2\""}]}`),
	}
	in, _, err := BuildInput("CompleteMultipartUpload", params, Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	cmi := in.(*s3.CompleteMultipartUploadInput)
	if len(cmi.MultipartUpload.Parts) != 2 || *cmi.MultipartUpload.Parts[0].PartNumber != 1 ||
		*cmi.MultipartUpload.Parts[1].ETag != `"e2"` {
		t.Errorf("nested parts wrong: %+v", cmi.MultipartUpload)
	}
	// RFC3339 timestamp param.
	in2, _, err := BuildInput("CopyObject", map[string]json.RawMessage{
		"Bucket": json.RawMessage(`"b"`), "Key": json.RawMessage(`"k"`),
		"CopySource":                json.RawMessage(`"b/src"`),
		"CopySourceIfModifiedSince": json.RawMessage(`"2026-01-02T03:04:05Z"`),
	}, Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	ci := in2.(*s3.CopyObjectInput)
	if ci.CopySourceIfModifiedSince == nil || !ci.CopySourceIfModifiedSince.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("timestamp = %v", ci.CopySourceIfModifiedSince)
	}
	if _, _, err := BuildInput("GetObject", map[string]json.RawMessage{"Nope": json.RawMessage(`1`)}, Resolver{}); err == nil {
		t.Error("unknown param must error")
	}
	if _, _, err := BuildInput("PutBucketLifecycle", nil, Resolver{}); err == nil {
		t.Error("unsupported op must error")
	}
}

func testClient(url string) *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint: aws.String(url),
		Region:       "us-east-1",
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("AK", "SK", ""),
		Retryer:      aws.NopRetryer{},
	})
}

func TestCallSuccessAndCapture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-amz-request-id", "REQ1")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version="1.0"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>UP123</UploadId></InitiateMultipartUploadResult>`))
	}))
	defer srv.Close()
	res, err := Call(context.Background(), testClient(srv.URL), "CreateMultipartUpload",
		map[string]json.RawMessage{"Bucket": json.RawMessage(`"b"`), "Key": json.RawMessage(`"k"`)}, Resolver{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatalf("call error: %v", res.Err)
	}
	if res.Status != 200 || res.Header.Get("x-amz-request-id") != "REQ1" {
		t.Errorf("raw capture: status %d headers %v", res.Status, res.Header)
	}
	out := res.Output.(map[string]any)
	if out["UploadId"] != "UP123" {
		t.Errorf("UploadId = %v", out["UploadId"])
	}
	if _, present := out["SSEKMSKeyId"]; present {
		t.Error("nil field should be absent from output")
	}
}

func TestCallErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`))
	}))
	defer srv.Close()
	res, err := Call(context.Background(), testClient(srv.URL), "GetObject",
		map[string]json.RawMessage{"Bucket": json.RawMessage(`"b"`), "Key": json.RawMessage(`"missing"`)}, Resolver{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil {
		t.Fatal("want call error")
	}
	if res.Status != 404 || res.Code != "NoSuchKey" {
		t.Errorf("status %d code %q msg %q", res.Status, res.Code, res.Msg)
	}
}

func TestCallHeadEmptyBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(412)
	}))
	defer srv.Close()
	res, err := Call(context.Background(), testClient(srv.URL), "HeadObject",
		map[string]json.RawMessage{"Bucket": json.RawMessage(`"b"`), "Key": json.RawMessage(`"k"`)}, Resolver{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil || res.Status != 412 {
		t.Errorf("want 412 error, got status %d err %v", res.Status, res.Err)
	}
}

func TestCallBodyDrain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.WriteHeader(200)
		w.Write([]byte("hello body"))
	}))
	defer srv.Close()
	res, err := Call(context.Background(), testClient(srv.URL), "GetObject",
		map[string]json.RawMessage{"Bucket": json.RawMessage(`"b"`), "Key": json.RawMessage(`"k"`)}, Resolver{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if string(res.Body) != "hello body" {
		t.Errorf("body = %q", res.Body)
	}
	out := res.Output.(map[string]any)
	if _, present := out["Body"]; present {
		t.Error("Body must be excluded from the generic output")
	}
	if out["ContentLength"] != int64(10) {
		t.Errorf("ContentLength = %v (%T)", out["ContentLength"], out["ContentLength"])
	}
	lm, _ := out["LastModified"].(string)
	if lm == "" {
		t.Errorf("LastModified missing: %v", out["LastModified"])
	}
	if _, err := time.Parse(time.RFC3339Nano, lm); err != nil {
		t.Errorf("LastModified %q is not RFC3339 (must round-trip into *time.Time params)", lm)
	}
}

// TestCallStreamsLargeBodyWithoutBuffering proves the streaming body path keeps
// memory bounded, and that the measurement can tell the difference: the same
// upload runs through the bytes path as a control and must allocate at least the
// dataset's size, where the streamed path allocates a small fraction of it.
//
// 64 MiB is deliberate: far enough above vdata.StreamThreshold to take the
// streaming branch, small enough for CI, and large enough that a regression to
// whole-body buffering is unmistakable. The 5 GiB corpus vector cannot be
// exercised here.
//
// The dataset is a $prng rather than the $pattern the corpus's own large vector
// uses: prng generation hashes every 32-byte block, so it is the kind that could
// allocate while producing the stream, which makes it the stronger case here.
func TestCallStreamsLargeBodyWithoutBuffering(t *testing.T) {
	const size = 64 << 20
	specs := map[string]s3vectors.DataSpec{
		"big": {Prng: &s3vectors.PrngData{Seed: "dispatch/stream-test", Size: size}},
	}
	want, err := datagen.Generate(specs, "big")
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(want)

	// The server digests as it reads; buffering 64 MiB here would pollute the
	// very measurement this test exists to make.
	var gotSum [sha256.Size]byte
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, err := io.Copy(h, r.Body)
		if err != nil {
			t.Errorf("server read body: %v", err)
		}
		gotLen = n
		copy(gotSum[:], h.Sum(nil))
		w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	params := map[string]json.RawMessage{
		"Bucket": json.RawMessage(`"b"`),
		"Key":    json.RawMessage(`"k"`),
		"Body":   json.RawMessage(`{"$data": "big"}`),
	}
	bytesResolver := func(name string) ([]byte, error) { return datagen.Generate(specs, name) }

	measure := func(r Resolver) uint64 {
		t.Helper()
		gotLen, gotSum = 0, [sha256.Size]byte{}
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		if _, err := Call(context.Background(), testClient(srv.URL), "PutObject", params, r, ""); err != nil {
			t.Fatalf("Call: %v", err)
		}
		runtime.ReadMemStats(&m1)
		if gotLen != size {
			t.Fatalf("server received %d bytes, want %d", gotLen, size)
		}
		if gotSum != wantSum {
			t.Fatal("server received different bytes than the dataset declares")
		}
		return m1.TotalAlloc - m0.TotalAlloc
	}

	buffered := measure(Resolver{Bytes: bytesResolver})
	streamed := measure(Resolver{
		Bytes: bytesResolver,
		Stream: func(name string) (io.ReadSeeker, int64, error) {
			r, err := datagen.NewReader(specs, name)
			if err != nil {
				return nil, 0, err
			}
			return r, r.Size(), nil
		},
	})

	t.Logf("allocated: buffered %.1f MiB, streamed %.1f MiB (payload %d MiB)",
		float64(buffered)/(1<<20), float64(streamed)/(1<<20), size>>20)

	// The control must buffer, or the comparison below proves nothing.
	if buffered < size {
		t.Errorf("control allocated %d bytes for a %d-byte body; expected it to buffer", buffered, size)
	}
	if streamed > size/4 {
		t.Errorf("streamed path allocated %d bytes for a %d-byte body; expected a small fraction (buffering regressed?)", streamed, size)
	}
}
