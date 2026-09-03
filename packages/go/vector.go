package s3tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"

	"github.com/cloud-portable/s3tests/packages/go/internal/interp"
	"github.com/cloud-portable/s3tests/packages/go/internal/vdata"
)

// vectorRun carries the mutable state of one executing vector.
type vectorRun struct {
	runner  *Runner
	vector  *s3vectors.Vector
	scope   *interp.Scope
	cache   *vdata.Cache
	buckets []BucketInfo
	result  VectorResult
}

// newResult seeds a result with the vector's identifying metadata.
func newResult(v *s3vectors.Vector) VectorResult {
	return VectorResult{ID: v.ID, Group: v.Group, Title: v.Title, Tags: v.Tags, Source: v.Source}
}

func (r *Runner) runVector(ctx context.Context, v *s3vectors.Vector) VectorResult {
	started := time.Now()
	cache := vdata.New(v.Data)
	vr := &vectorRun{
		runner: r,
		vector: v,
		cache:  cache,
		scope: &interp.Scope{
			Env:  map[string]string{"endpoint": r.cfg.Endpoint, "region": r.cfg.Region},
			Res:  map[string]map[string]string{},
			Cap:  map[string]string{},
			Data: cache.Derived,
		},
		result: newResult(v),
	}
	vr.run(ctx)
	vr.result.Duration = time.Since(started)
	return vr.result
}

func (vr *vectorRun) run(ctx context.Context) {
	defer vr.teardown(ctx)

	for i := range vr.vector.Prerequisites {
		if err := vr.establish(ctx, &vr.vector.Prerequisites[i]); err != nil {
			vr.result.Outcome = Blocked
			vr.result.Reason = err.Error()
			return
		}
	}

	for i := range vr.vector.Steps {
		sr := vr.runStep(ctx, i, &vr.vector.Steps[i])
		vr.result.Steps = append(vr.result.Steps, sr)
		if !sr.Passed {
			vr.result.Outcome = Fail
			return
		}
	}
	vr.result.Outcome = Pass
}

// establish provisions one prerequisite and registers its resource
// attributes in the scope.
func (vr *vectorRun) establish(ctx context.Context, p *s3vectors.Prerequisite) error {
	target := vr.runner.target
	switch {
	case p.Bucket != nil:
		name := vr.bucketName(p.Bucket.Handle)
		info, err := vr.runner.cfg.Provisioner.Bucket(ctx, target, p.Bucket, name)
		if err != nil {
			return fmt.Errorf("prerequisite $bucket %s: %w", p.Bucket.Handle, err)
		}
		vr.buckets = append(vr.buckets, info)
		vr.scope.Res[p.Bucket.Handle] = map[string]string{"name": info.Name}
	case p.Object != nil:
		bucketAttrs, ok := vr.scope.Res[p.Object.Bucket]
		if !ok {
			return fmt.Errorf("prerequisite $object %s: unknown bucket handle %q", p.Object.Handle, p.Object.Bucket)
		}
		op, body, err := vr.resolveObjectPrereq(p.Object)
		if err != nil {
			return fmt.Errorf("prerequisite $object %s: %w", p.Object.Handle, err)
		}
		info, err := vr.runner.cfg.Provisioner.Object(ctx, target, op, bucketAttrs["name"], body)
		if err != nil {
			return fmt.Errorf("prerequisite $object %s: %w", p.Object.Handle, err)
		}
		vr.scope.Res[p.Object.Handle] = map[string]string{
			"key": info.Key, "etag": info.ETag, "versionId": info.VersionID,
		}
		vr.trackKey(bucketAttrs["name"], info.Key)
	case p.Credential != nil:
		cred, err := vr.runner.ids.provisionAlt(ctx, p.Credential.Handle)
		if err != nil {
			return fmt.Errorf("prerequisite $credential %s: %w", p.Credential.Handle, err)
		}
		vr.scope.Res[p.Credential.Handle] = map[string]string{
			"accessKeyId": cred.AccessKeyID, "canonicalId": cred.CanonicalID, "displayName": cred.DisplayName,
		}
	default:
		return fmt.Errorf("prerequisite with no $bucket/$object/$credential key")
	}
	return nil
}

// resolveObjectPrereq interpolates the object prerequisite's string fields
// and resolves its body content descriptor (without mutating the shared
// corpus struct).
func (vr *vectorRun) resolveObjectPrereq(p *s3vectors.ObjectPrerequisite) (*s3vectors.ObjectPrerequisite, []byte, error) {
	out := *p
	var err error
	if out.Key, err = vr.scope.String(p.Key); err != nil {
		return nil, nil, err
	}
	if out.ContentType, err = vr.scope.String(p.ContentType); err != nil {
		return nil, nil, err
	}
	if len(p.Metadata) > 0 {
		out.Metadata = make(map[string]string, len(p.Metadata))
		for k, v := range p.Metadata {
			if out.Metadata[k], err = vr.scope.String(v); err != nil {
				return nil, nil, err
			}
		}
	}
	var body []byte
	if len(p.Body) > 0 {
		raw, err := vr.scope.Raw(p.Body)
		if err != nil {
			return nil, nil, err
		}
		if body, err = vr.resolveContent(raw); err != nil {
			return nil, nil, fmt.Errorf("body: %w", err)
		}
	}
	return &out, body, nil
}

// runStep executes one step; sr.Passed reflects the outcome.
func (vr *vectorRun) runStep(ctx context.Context, index int, step *s3vectors.Step) StepResult {
	started := time.Now()
	var sr StepResult
	sr.Index = index
	switch {
	case step.Operation != nil:
		vr.runOperationStep(ctx, step.Operation, &sr)
	case step.HTTP != nil:
		vr.runHTTPStep(ctx, step.HTTP, &sr)
	default:
		sr.Err = "step with no $operation/$http key"
		vr.result.RunnerError = sr.Err
	}
	sr.Duration = time.Since(started)
	sr.Passed = sr.Err == "" && len(sr.Failures) == 0
	return sr
}

// interpolateInto round-trips a step struct through JSON to interpolate every
// string value against the current scope, leaving the shared corpus value
// untouched.
func (vr *vectorRun) interpolateInto(src, dst any) error {
	raw, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("re-marshaling step: %w", err)
	}
	iraw, err := vr.scope.Raw(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(iraw, dst)
}

// resolveContent decodes an interpolated content descriptor into bytes.
func (vr *vectorRun) resolveContent(raw json.RawMessage) ([]byte, error) {
	return contentFromRaw(raw, vr.cache)
}

// trackBucket registers a bucket created by a *step* (CreateBucket, or a raw
// PUT on a bucket path) so teardown covers it like prerequisite buckets.
func (vr *vectorRun) trackBucket(name string) {
	if name == "" {
		return
	}
	for _, b := range vr.buckets {
		if b.Name == name {
			return
		}
	}
	vr.buckets = append(vr.buckets, BucketInfo{Name: name})
}

// trackKey records an object key the runner wrote, giving teardown a way to
// delete keys that server listings fail to surface.
func (vr *vectorRun) trackKey(bucket, key string) {
	if bucket == "" || key == "" {
		return
	}
	for i := range vr.buckets {
		if vr.buckets[i].Name != bucket {
			continue
		}
		if !slices.Contains(vr.buckets[i].KnownKeys, key) {
			vr.buckets[i].KnownKeys = append(vr.buckets[i].KnownKeys, key)
		}
		return
	}
}

// runnerFail records a runner/vector-definition error (not a compat failure).
func (vr *vectorRun) runnerFail(sr *StepResult, err error) {
	sr.Err = err.Error()
	vr.result.RunnerError = err.Error()
}

// bucketName picks a unique, valid bucket name: prefix + vector id + handle +
// random suffix, lowercased and trimmed to the 63-char limit.
func (vr *vectorRun) bucketName(handle string) string {
	var suffix [4]byte
	rand.Read(suffix[:])
	name := vr.runner.cfg.BucketPrefix + vr.vector.ID + "-" + handle
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, name)
	tail := "-" + hex.EncodeToString(suffix[:])
	if len(name)+len(tail) > 63 {
		name = name[:63-len(tail)]
	}
	return name + tail
}

// teardown releases the vector's buckets; problems become warnings.
func (vr *vectorRun) teardown(ctx context.Context) {
	if vr.runner.cfg.KeepResources || len(vr.buckets) == 0 {
		return
	}
	// Cancellation must not leak buckets.
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	for _, err := range vr.runner.cfg.Provisioner.Teardown(tctx, vr.runner.target, vr.buckets) {
		vr.result.Warnings = append(vr.result.Warnings, err.Error())
	}
}
