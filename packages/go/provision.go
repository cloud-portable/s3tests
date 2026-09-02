package s3tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/aws/smithy-go"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// BucketInfo is a provisioned $bucket prerequisite; Name feeds
// ${res.<handle>.name}.
type BucketInfo struct {
	Name string
	// KnownKeys are object keys the runner observed being written to this
	// bucket. Teardown deletes them explicitly before the listing-based
	// sweep: some servers' listings miss keys (e.g. MinIO hides "foo/bar"
	// when object "foo" exists), which would otherwise leak the bucket.
	KnownKeys []string
}

// ObjectInfo is a provisioned $object prerequisite; the fields feed
// ${res.<handle>.key|etag|versionId}.
type ObjectInfo struct {
	Key       string
	ETag      string
	VersionID string
}

// Provisioner establishes vector prerequisites and tears their resources
// down. Implementations may target the server's own API (see
// DefaultProvisioner) or any out-of-band mechanism.
type Provisioner interface {
	// Bucket provisions a bucket named name per the prerequisite spec
	// (versioning "": unset, "Enabled" or "Suspended"; ObjectLock enabled
	// at creation). The runner chooses name.
	Bucket(ctx context.Context, t Target, p *s3vectors.BucketPrerequisite, name string) (BucketInfo, error)
	// Object seeds bucketName with an object. body holds the resolved
	// content-descriptor bytes (nil when the prerequisite declares none —
	// still create the object, empty).
	Object(ctx context.Context, t Target, p *s3vectors.ObjectPrerequisite, bucketName string, body []byte) (ObjectInfo, error)
	// Teardown removes the vector's buckets and their contents,
	// best-effort. Returned errors become VectorResult.Warnings, never
	// failures (e.g. COMPLIANCE-retained objects are legitimately
	// undeletable).
	Teardown(ctx context.Context, t Target, buckets []BucketInfo) []error
}

// DefaultProvisioner provisions prerequisites against the endpoint under
// test itself, using the main identity.
type DefaultProvisioner struct{}

var _ Provisioner = DefaultProvisioner{}

func (DefaultProvisioner) Bucket(ctx context.Context, t Target, p *s3vectors.BucketPrerequisite, name string) (BucketInfo, error) {
	in := &s3.CreateBucketInput{Bucket: aws.String(name)}
	if p.ObjectLock != nil && *p.ObjectLock {
		in.ObjectLockEnabledForBucket = aws.Bool(true)
	}
	if t.Region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(t.Region),
		}
	}
	if _, err := t.Client.CreateBucket(ctx, in); err != nil {
		return BucketInfo{}, fmt.Errorf("CreateBucket %s: %w", name, err)
	}
	if p.Versioning != "" {
		_, err := t.Client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: aws.String(name),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatus(p.Versioning),
			},
		})
		if err != nil {
			return BucketInfo{}, fmt.Errorf("PutBucketVersioning %s=%s: %w", name, p.Versioning, err)
		}
	}
	return BucketInfo{Name: name}, nil
}

func (DefaultProvisioner) Object(ctx context.Context, t Target, p *s3vectors.ObjectPrerequisite, bucketName string, body []byte) (ObjectInfo, error) {
	in := &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(p.Key),
		Body:   bytes.NewReader(body),
	}
	if p.ContentType != "" {
		in.ContentType = aws.String(p.ContentType)
	}
	if len(p.Metadata) > 0 {
		in.Metadata = p.Metadata
	}
	out, err := t.Client.PutObject(ctx, in)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("PutObject %s/%s: %w", bucketName, p.Key, err)
	}
	return ObjectInfo{
		Key:       p.Key,
		ETag:      aws.ToString(out.ETag),
		VersionID: aws.ToString(out.VersionId),
	}, nil
}

func (DefaultProvisioner) Teardown(ctx context.Context, t Target, buckets []BucketInfo) []error {
	var errs []error
	for _, b := range buckets {
		errs = append(errs, teardownBucket(ctx, t.Client, b.Name, b.KnownKeys)...)
	}
	return errs
}

// teardownBucket empties and deletes one bucket, best-effort: abort multipart
// uploads, delete every version and delete marker (bypassing governance
// retention and lifting legal holds on object-lock buckets), then delete the
// bucket itself.
func teardownBucket(ctx context.Context, client *s3.Client, bucket string, knownKeys []string) []error {
	var errs []error

	// Abort in-flight multipart uploads.
	var keyMarker, uploadIDMarker *string
	for {
		mu, err := client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket: aws.String(bucket), KeyMarker: keyMarker, UploadIdMarker: uploadIDMarker,
		})
		if isNoSuchBucket(err) {
			return nil // already gone (e.g. a step deleted it) — nothing to do
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("teardown %s: ListMultipartUploads: %w", bucket, err))
			break
		}
		for _, u := range mu.Uploads {
			if _, err := client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket: aws.String(bucket), Key: u.Key, UploadId: u.UploadId,
			}); err != nil {
				errs = append(errs, fmt.Errorf("teardown %s: AbortMultipartUpload %s: %w", bucket, aws.ToString(u.Key), err))
			}
		}
		if mu.IsTruncated == nil || !*mu.IsTruncated {
			break
		}
		keyMarker, uploadIDMarker = mu.NextKeyMarker, mu.NextUploadIdMarker
	}

	// Delete keys the runner knows it wrote, in case the server's listings
	// miss them (best-effort; the sweep below reports anything that fails).
	for _, key := range knownKeys {
		client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	}

	// AWS rejects BypassGovernanceRetention on buckets without object lock,
	// so only send it (and lift legal holds) where lock is configured.
	locked := bucketHasObjectLock(ctx, client, bucket)

	var keyM, verM *string
	for {
		lv, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket: aws.String(bucket), KeyMarker: keyM, VersionIdMarker: verM,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("teardown %s: ListObjectVersions: %w", bucket, err))
			break
		}
		var ids []s3types.ObjectIdentifier
		for _, v := range lv.Versions {
			if locked {
				// Best-effort: legal holds block deletion even with bypass.
				client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
					Bucket: aws.String(bucket), Key: v.Key, VersionId: v.VersionId,
					LegalHold: &s3types.ObjectLockLegalHold{Status: s3types.ObjectLockLegalHoldStatusOff},
				})
			}
			ids = append(ids, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range lv.DeleteMarkers {
			ids = append(ids, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		for chunk := range slicesChunk(ids, 1000) {
			in := &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &s3types.Delete{Objects: chunk, Quiet: aws.Bool(true)},
			}
			if locked {
				in.BypassGovernanceRetention = aws.Bool(true)
			}
			out, err := client.DeleteObjects(ctx, in)
			if err != nil {
				errs = append(errs, fmt.Errorf("teardown %s: DeleteObjects: %w", bucket, err))
				continue
			}
			for _, e := range out.Errors {
				errs = append(errs, fmt.Errorf("teardown %s: delete %s (%s): %s",
					bucket, aws.ToString(e.Key), aws.ToString(e.VersionId), aws.ToString(e.Message)))
			}
		}
		if lv.IsTruncated == nil || !*lv.IsTruncated {
			break
		}
		keyM, verM = lv.NextKeyMarker, lv.NextVersionIdMarker
	}

	if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil && !isNoSuchBucket(err) {
		errs = append(errs, fmt.Errorf("teardown %s: DeleteBucket: %w", bucket, err))
	}
	return errs
}

func isNoSuchBucket(err error) bool {
	if err == nil {
		return false
	}
	var ae smithy.APIError
	return errors.As(err, &ae) && (ae.ErrorCode() == "NoSuchBucket" || ae.ErrorCode() == "NotFound")
}

func bucketHasObjectLock(ctx context.Context, client *s3.Client, bucket string) bool {
	out, err := client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: aws.String(bucket)})
	if err != nil {
		// Any error (typically ObjectLockConfigurationNotFoundError) means:
		// don't send lock-specific teardown requests.
		return false
	}
	return out.ObjectLockConfiguration != nil &&
		out.ObjectLockConfiguration.ObjectLockEnabled == s3types.ObjectLockEnabledEnabled
}

// slicesChunk yields s in chunks of at most n.
func slicesChunk(s []s3types.ObjectIdentifier, n int) func(func([]s3types.ObjectIdentifier) bool) {
	return func(yield func([]s3types.ObjectIdentifier) bool) {
		for len(s) > 0 {
			m := min(n, len(s))
			if !yield(s[:m]) {
				return
			}
			s = s[m:]
		}
	}
}
