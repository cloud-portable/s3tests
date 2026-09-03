package s3tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	s3vectors "github.com/alanshaw/s3vectors/packages/go"

	"github.com/alanshaw/s3tests/packages/go/internal/dispatch"
	"github.com/alanshaw/s3tests/packages/go/internal/jsonpath"
	"github.com/alanshaw/s3tests/packages/go/internal/match"
	"github.com/alanshaw/s3tests/packages/go/internal/rawhttp"
	"github.com/alanshaw/s3tests/packages/go/internal/vdata"
)

func contentFromRaw(raw json.RawMessage, cache *vdata.Cache) ([]byte, error) {
	return match.Content(raw, cache.Bytes)
}

func (vr *vectorRun) runOperationStep(ctx context.Context, src *s3vectors.OperationStep, sr *StepResult) {
	var op s3vectors.OperationStep
	if err := vr.interpolateInto(src, &op); err != nil {
		vr.runnerFail(sr, err)
		return
	}
	identity := op.Identity
	if identity == "" {
		identity = identityMain
	}
	sr.Kind, sr.Name, sr.Identity = "operation", op.Name, identity

	if op.Presign != nil {
		sr.Presigned = true
		vr.runPresignedStep(ctx, &op, identity, sr)
		return
	}

	client, err := vr.runner.ids.client(ctx, identity)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	res, err := dispatch.Call(ctx, client, op.Name, op.Params, vr.cache.Bytes)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	sr.Status = res.Status
	if res.Err == nil {
		var bucket, key string
		json.Unmarshal(op.Params["Bucket"], &bucket)
		json.Unmarshal(op.Params["Key"], &key)
		switch op.Name {
		case "CreateBucket":
			vr.trackBucket(bucket)
		case "PutObject", "CopyObject", "CompleteMultipartUpload":
			vr.trackKey(bucket, key)
		}
	}
	vr.evaluateOperation(&op, res, sr)
	if len(sr.Failures) > 0 || sr.Err != "" {
		return
	}
	vr.capture(op.Capture, res.Output, sr)
}

// capture evaluates capture paths against a generic value and registers the
// results for later steps.
func (vr *vectorRun) capture(spec map[string]string, value any, sr *StepResult) {
	if len(spec) == 0 {
		return
	}
	sr.Captured = map[string]string{}
	for name, path := range spec {
		val, err := jsonpath.GetString(value, path)
		if err != nil {
			sr.Err = fmt.Sprintf("capture %s: %v", name, err)
			return
		}
		sr.Captured[name] = val
		vr.scope.Cap[name] = val
	}
}

// evaluateOperation checks an $operation step's expectations against the
// dispatch result.
func (vr *vectorRun) evaluateOperation(op *s3vectors.OperationStep, res *dispatch.Result, sr *StepResult) {
	exp := op.Expect
	expectsError := exp != nil && len(exp.Error) > 0

	if expectsError && res.Err == nil {
		sr.Failures = append(sr.Failures, CheckFailure{
			Field: "error", Expected: renderRaw(exp.Error),
			Actual: fmt.Sprintf("success (status %d)", res.Status),
		})
		return
	}
	if !expectsError && res.Err != nil {
		// A non-2xx expect.status without expect.error (e.g. 304 responses,
		// which surface as SDK errors) is still a pass when the status and
		// remaining assertions hold.
		statusTolerated := exp != nil && exp.Status != 0 && exp.Status == res.Status
		if !statusTolerated {
			sr.Failures = append(sr.Failures, CheckFailure{
				Field: "error", Expected: "success",
				Actual: fmt.Sprintf("status %d, error %s: %s", res.Status, res.Code, res.Msg),
			})
			return
		}
	}
	if exp == nil {
		return
	}
	if exp.Status != 0 && res.Status != exp.Status {
		sr.Failures = append(sr.Failures, CheckFailure{
			Field: "status", Expected: fmt.Sprint(exp.Status), Actual: fmt.Sprint(res.Status),
		})
	}
	if expectsError {
		vr.matchError(exp.Error, res.Code, res.Msg, res.Status, strings.HasPrefix(op.Name, "Head"), sr)
	}
	vr.matchHeaders(exp.Headers, res.Header, sr)
	if len(exp.Response) > 0 {
		expected, err := match.Decode(exp.Response)
		if err != nil {
			vr.runnerFail(sr, fmt.Errorf("expect.response: %w", err))
			return
		}
		for _, m := range match.Value("", expected, res.Output, res.Output != nil) {
			sr.Failures = append(sr.Failures, CheckFailure{Field: "response." + m.Path, Expected: m.Expected, Actual: m.Actual})
		}
	}
	vr.matchBody(exp.Body, res.Body, sr)
}

// statusImpliedCodes maps statuses whose error code cannot appear on the wire
// (HEAD responses and 304s have no body) to the codes they imply.
var statusImpliedCodes = map[int][]string{
	304: {"NotModified", ""},
	404: {"NotFound", "NoSuchKey", "NoSuchBucket"},
	405: {"MethodNotAllowed"},
	412: {"PreconditionFailed", ""},
}

func (vr *vectorRun) matchError(expected json.RawMessage, code, msg string, status int, bodyless bool, sr *StepResult) {
	mismatches, err := match.Error(expected, code, msg)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	if len(mismatches) > 0 && (bodyless || status == 304) {
		// The wire response carried no error document; accept the expected
		// code when the status implies it.
		if want := expectedErrorCode(expected); want != "" {
			if implied, ok := statusImpliedCodes[status]; ok &&
				slices.Contains(implied, want) && slices.Contains(implied, code) {
				return
			}
		}
	}
	for _, m := range mismatches {
		sr.Failures = append(sr.Failures, CheckFailure{Field: m.Path, Expected: m.Expected, Actual: m.Actual})
	}
}

func expectedErrorCode(raw json.RawMessage) string {
	v, err := match.Decode(raw)
	if err != nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		s, _ := t["code"].(string)
		return s
	}
	return ""
}

func (vr *vectorRun) matchHeaders(expected map[string]json.RawMessage, hdr http.Header, sr *StepResult) {
	if len(expected) == 0 {
		return
	}
	if hdr == nil {
		hdr = http.Header{}
	}
	mismatches, err := match.Headers(expected, hdr)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	for _, m := range mismatches {
		sr.Failures = append(sr.Failures, CheckFailure{Field: m.Path, Expected: m.Expected, Actual: m.Actual})
	}
}

func (vr *vectorRun) matchBody(expected json.RawMessage, body []byte, sr *StepResult) {
	if len(expected) == 0 {
		return
	}
	mismatches, err := match.Body(expected, body, vr.cache.Bytes)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	for _, m := range mismatches {
		sr.Failures = append(sr.Failures, CheckFailure{Field: m.Path, Expected: m.Expected, Actual: m.Actual})
	}
}

// runPresignedStep mints a presigned URL for the operation and executes it
// with a plain HTTP client. Expectations are evaluated against the raw
// response (the corpus never asserts `response` on presigned steps).
func (vr *vectorRun) runPresignedStep(ctx context.Context, op *s3vectors.OperationStep, identity string, sr *StepResult) {
	pc, err := vr.runner.ids.presignClient(ctx, identity)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	expires := time.Duration(op.Presign.ExpiresIn) * time.Second
	preq, body, err := dispatch.Presign(ctx, pc, op.Name, op.Params, vr.cache.Bytes, expires)
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, preq.Method, preq.URL, bytes.NewReader(body))
	if err != nil {
		vr.runnerFail(sr, err)
		return
	}
	for name, vals := range preq.SignedHeader {
		for _, v := range vals {
			req.Header.Add(name, v)
		}
	}
	resp, err := vr.runner.cfg.HTTPClient.Do(req)
	if err != nil {
		sr.Err = fmt.Sprintf("executing presigned request: %v", err)
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		sr.Err = fmt.Sprintf("reading presigned response: %v", err)
		return
	}
	sr.Status = resp.StatusCode
	vr.evaluateRaw(op.Expect, resp.StatusCode, resp.Header, respBody, sr)
	if len(sr.Failures) > 0 || sr.Err != "" {
		return
	}
	vr.capture(op.Capture, rawCaptureValue(resp.StatusCode, resp.Header), sr)
}

// evaluateRaw checks expectations for steps executed outside the SDK
// ($http and presigned): status, headers, XML error body, body bytes.
func (vr *vectorRun) evaluateRaw(exp *s3vectors.Expect, status int, hdr http.Header, body []byte, sr *StepResult) {
	expectsError := exp != nil && len(exp.Error) > 0
	code, msg := rawhttp.ParseXMLError(body)

	if expectsError && status < 400 && code == "" {
		sr.Failures = append(sr.Failures, CheckFailure{
			Field: "error", Expected: renderRaw(exp.Error), Actual: fmt.Sprintf("success (status %d)", status),
		})
		return
	}
	if !expectsError && (exp == nil || exp.Status == 0) && (status < 200 || status > 299) {
		sr.Failures = append(sr.Failures, CheckFailure{
			Field: "status", Expected: "2xx", Actual: fmt.Sprintf("%d %s %s", status, code, msg),
		})
		return
	}
	if exp == nil {
		return
	}
	if exp.Status != 0 && status != exp.Status {
		sr.Failures = append(sr.Failures, CheckFailure{
			Field: "status", Expected: fmt.Sprint(exp.Status), Actual: fmt.Sprint(status),
		})
	}
	if expectsError {
		vr.matchError(exp.Error, code, msg, status, false, sr)
	}
	vr.matchHeaders(exp.Headers, hdr, sr)
	if len(exp.Response) > 0 {
		vr.runnerFail(sr, fmt.Errorf("expect.response is not supported on raw HTTP/presigned steps"))
		return
	}
	vr.matchBody(exp.Body, body, sr)
}

// rawCaptureValue is the capture-path root for $http/presigned steps:
// {status, headers} with lowercased header names (first value).
func rawCaptureValue(status int, hdr http.Header) any {
	headers := map[string]any{}
	for name, vals := range hdr {
		if len(vals) > 0 {
			headers[strings.ToLower(name)] = vals[0]
		}
	}
	return map[string]any{"status": int64(status), "headers": headers}
}

func renderRaw(raw json.RawMessage) string {
	return string(raw)
}
