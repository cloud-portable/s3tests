package s3tests

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"

	"github.com/cloud-portable/s3tests/packages/go/internal/rawhttp"
)

func (vr *vectorRun) runHTTPStep(ctx context.Context, src *s3vectors.HTTPStep, sr *StepResult) {
	var st s3vectors.HTTPStep
	if err := vr.interpolateInto(src, &st); err != nil {
		vr.runnerFail(sr, err)
		return
	}
	identity := st.Identity
	if identity == "" {
		identity = identityMain
	}
	sr.Kind, sr.Name, sr.Identity = "http", st.Method+" "+st.Path, identity

	// sign defaults to true; anonymous requests are inherently unsigned.
	sign := (st.Sign == nil || *st.Sign) && identity != identityAnonymous
	var creds aws.Credentials
	if sign {
		var err error
		creds, err = vr.runner.ids.retrieve(ctx, identity)
		if err != nil {
			vr.runnerFail(sr, err)
			return
		}
	}

	var body []byte
	if len(st.Body) > 0 {
		var err error
		body, err = vr.resolveContent(st.Body)
		if err != nil {
			vr.runnerFail(sr, fmt.Errorf("body: %w", err))
			return
		}
	}

	req := &rawhttp.Request{
		Method:  st.Method,
		Path:    st.Path,
		Query:   oneOrManyMap(st.Query),
		Headers: orderedHeaders(st.Headers),
		Body:    body,
		Sign:    sign,
		Creds:   creds,
		Region:  vr.runner.cfg.Region,
	}
	res, err := rawhttp.Do(ctx, vr.runner.cfg.Endpoint, req)
	if err != nil {
		// A transport-level failure is an observation about the server, not
		// a runner bug: report it as the step's failure.
		sr.Err = err.Error()
		return
	}
	sr.Status = res.Status
	// A successful raw PUT on a bare bucket path is a bucket creation the
	// teardown must cover.
	if st.Method == "PUT" && res.Status >= 200 && res.Status < 300 && len(st.Query) == 0 {
		if seg := strings.Trim(st.Path, "/"); seg != "" && !strings.Contains(seg, "/") {
			vr.trackBucket(seg)
		}
	}
	vr.evaluateRaw(st.Expect, res.Status, res.Header, res.Body, sr)
	if len(sr.Failures) > 0 || sr.Err != "" {
		return
	}
	vr.capture(st.Capture, rawCaptureValue(res.Status, res.Header), sr)
}

func oneOrManyMap(m map[string]s3vectors.OneOrMany) map[string][]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// orderedHeaders flattens the step's header map into a deterministic list
// (JSON objects carry no order, so keys are sorted; multi-valued headers keep
// their declared value order).
func orderedHeaders(m map[string]s3vectors.OneOrMany) []rawhttp.Header {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []rawhttp.Header
	for _, name := range names {
		for _, v := range m[name] {
			out = append(out, rawhttp.Header{Name: name, Value: v})
		}
	}
	return out
}
