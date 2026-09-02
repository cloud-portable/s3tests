package s3tests

// The offline corpus smoke test walks every api vector in the embedded
// corpus — no network — and proves the runner can execute it: every
// placeholder resolves, every operation decodes into an SDK input, every
// $matches regex compiles, every capture path parses, every content
// descriptor and dataset materializes. It is the drift alarm for corpus
// version bumps.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"

	"github.com/cloud-portable/s3tests/packages/go/internal/dispatch"
	"github.com/cloud-portable/s3tests/packages/go/internal/interp"
	"github.com/cloud-portable/s3tests/packages/go/internal/jsonpath"
	"github.com/cloud-portable/s3tests/packages/go/internal/match"
	"github.com/cloud-portable/s3tests/packages/go/internal/vdata"
)

var capRefPattern = regexp.MustCompile(`\$\{cap\.([A-Za-z0-9_-]+)\}`)

func TestCorpusSmoke(t *testing.T) {
	files, err := s3vectors.All()
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	apiCount := 0
	for _, f := range files {
		for i := range f.Vectors {
			v := &f.Vectors[i]
			if !v.IsAPI() {
				continue
			}
			apiCount++
			for _, p := range smokeVector(v) {
				problems = append(problems, fmt.Sprintf("%s: %s", v.ID, p))
			}
		}
	}
	if apiCount < 1000 {
		t.Errorf("only %d api vectors found — corpus load broken?", apiCount)
	}
	// The corpus's single known runner limitation: lifecycle-config-0010
	// step 2 uses PutBucketLifecycle, which aws-sdk-go-v2 dropped.
	allowed := map[string]bool{
		"lifecycle-config-0010: step 2: operation PutBucketLifecycle is not supported by aws-sdk-go-v2 service/s3": true,
	}
	var unexpected []string
	for _, p := range problems {
		if !allowed[p] {
			unexpected = append(unexpected, p)
		}
	}
	if len(unexpected) > 0 {
		for _, p := range unexpected {
			t.Errorf("%s", p)
		}
		t.Fatalf("%d unexpected problems across %d api vectors", len(unexpected), apiCount)
	}
	if len(problems) != len(allowed) {
		t.Errorf("expected exactly %d known problem(s), found %d: %v", len(allowed), len(problems), problems)
	}
}

// smokeVector dry-runs one vector and returns everything the runner could
// not process.
func smokeVector(v *s3vectors.Vector) []string {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	cache := vdata.New(v.Data)
	scope := &interp.Scope{
		Env:  map[string]string{"endpoint": "http://smoke.invalid:9000", "region": "us-east-1"},
		Res:  map[string]map[string]string{},
		Cap:  map[string]string{},
		Data: cache.Derived,
	}

	// Register prerequisite resource attributes exactly as the runner would.
	for pi := range v.Prerequisites {
		p := &v.Prerequisites[pi]
		switch {
		case p.Bucket != nil:
			scope.Res[p.Bucket.Handle] = map[string]string{"name": "smoke-bucket"}
		case p.Object != nil:
			scope.Res[p.Object.Handle] = map[string]string{
				"key": p.Object.Key, "etag": `"d41d8cd98f00b204e9800998ecf8427e"`, "versionId": "smoke-version",
			}
			if len(p.Object.Body) > 0 {
				raw, err := scope.Raw(p.Object.Body)
				if err != nil {
					fail("object prerequisite %s body: %v", p.Object.Handle, err)
				} else if _, err := match.Content(raw, cache.Bytes); err != nil {
					fail("object prerequisite %s body: %v", p.Object.Handle, err)
				}
			}
		case p.Credential != nil:
			scope.Res[p.Credential.Handle] = map[string]string{
				"accessKeyId": "SMOKEKEY", "canonicalId": "smoke-canonical", "displayName": "smoke",
			}
		default:
			fail("prerequisite %d has no union key", pi)
		}
	}

	// Pre-seed every ${cap.*} reference (capture values are runtime-only).
	rawVector, err := json.Marshal(v)
	if err != nil {
		return append(problems, fmt.Sprintf("re-marshal: %v", err))
	}
	// The seed value must survive every context captures are re-injected
	// into, including *time.Time params (captured LastModified values).
	for _, m := range capRefPattern.FindAllStringSubmatch(string(rawVector), -1) {
		scope.Cap[m[1]] = "2026-01-01T00:00:00Z"
	}

	for si := range v.Steps {
		step := &v.Steps[si]
		stepNo := si + 1
		switch {
		case step.Operation != nil:
			src := step.Operation
			raw, err := json.Marshal(src)
			if err != nil {
				fail("step %d: re-marshal: %v", stepNo, err)
				continue
			}
			iraw, err := scope.Raw(raw)
			if err != nil {
				fail("step %d: %v", stepNo, err)
				continue
			}
			var op s3vectors.OperationStep
			if err := json.Unmarshal(iraw, &op); err != nil {
				fail("step %d: %v", stepNo, err)
				continue
			}
			if _, _, err := dispatch.BuildInput(op.Name, op.Params, cache.Bytes); err != nil {
				fail("step %d: %v", stepNo, err)
			}
			if op.Presign != nil && !dispatch.PresignSupported(op.Name) {
				fail("step %d: operation %s cannot be presigned", stepNo, op.Name)
			}
			smokeExpect(op.Expect, cache, fail, stepNo)
			smokeCapture(op.Capture, fail, stepNo)
		case step.HTTP != nil:
			src := step.HTTP
			raw, err := json.Marshal(src)
			if err != nil {
				fail("step %d: re-marshal: %v", stepNo, err)
				continue
			}
			iraw, err := scope.Raw(raw)
			if err != nil {
				fail("step %d: %v", stepNo, err)
				continue
			}
			var st s3vectors.HTTPStep
			if err := json.Unmarshal(iraw, &st); err != nil {
				fail("step %d: %v", stepNo, err)
				continue
			}
			if len(st.Body) > 0 {
				if _, err := match.Content(st.Body, cache.Bytes); err != nil {
					fail("step %d: body: %v", stepNo, err)
				}
			}
			smokeExpect(st.Expect, cache, fail, stepNo)
			smokeCapture(st.Capture, fail, stepNo)
		default:
			fail("step %d has no union key", stepNo)
		}
	}
	return problems
}

func smokeCapture(spec map[string]string, fail func(string, ...any), stepNo int) {
	for name, path := range spec {
		if _, err := jsonpath.Parse(path); err != nil {
			fail("step %d: capture %s: %v", stepNo, name, err)
		}
	}
}

func smokeExpect(exp *s3vectors.Expect, cache *vdata.Cache, fail func(string, ...any), stepNo int) {
	if exp == nil {
		return
	}
	check := func(field string, raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		v, err := match.Decode(raw)
		if err != nil {
			fail("step %d: expect.%s: %v", stepNo, field, err)
			return
		}
		compileMatchers(v, func(err error) { fail("step %d: expect.%s: %v", stepNo, field, err) })
	}
	check("error", exp.Error)
	check("response", exp.Response)
	for name, raw := range exp.Headers {
		check("headers."+name, raw)
	}
	if len(exp.Body) > 0 {
		v, err := match.Decode(exp.Body)
		if err != nil {
			fail("step %d: expect.body: %v", stepNo, err)
			return
		}
		if m, ok := v.(map[string]any); ok {
			_, isDigest := m["$size"]
			_, isMD5 := m["$md5"]
			_, isSHA := m["$sha256"]
			if isDigest || isMD5 || isSHA {
				return
			}
		}
		if _, err := match.ContentValue(v, cache.Bytes); err != nil {
			fail("step %d: expect.body: %v", stepNo, err)
		}
	}
}

// compileMatchers walks a decoded matcher and compiles every $matches
// pattern.
func compileMatchers(v any, onErr func(error)) {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if k == "$matches" {
				if pat, ok := e.(string); ok {
					if err := match.CompileRegex(pat); err != nil {
						onErr(fmt.Errorf("$matches %q: %w", pat, err))
					}
				}
				continue
			}
			compileMatchers(e, onErr)
		}
	case []any:
		for _, e := range t {
			compileMatchers(e, onErr)
		}
	}
}
