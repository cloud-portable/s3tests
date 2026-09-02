package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/cloud-portable/s3tests/packages/go/internal/match"
)

var presignClientType = reflect.TypeFor[*s3.PresignClient]()

// PresignSupported reports whether the operation has a Presign method on
// s3.PresignClient.
func PresignSupported(name string) bool {
	_, ok := presignClientType.MethodByName("Presign" + name)
	return ok
}

// Presign mints a presigned request for the named operation. The body bytes
// (for presigned PUTs) are returned separately: S3 presigned requests use
// UNSIGNED-PAYLOAD, and the caller sends the body itself when executing.
func Presign(ctx context.Context, pc *s3.PresignClient, name string, params map[string]json.RawMessage, resolve match.ContentResolver, expires time.Duration) (*v4.PresignedHTTPRequest, []byte, error) {
	m := reflect.ValueOf(pc).MethodByName("Presign" + name)
	if !m.IsValid() {
		return nil, nil, fmt.Errorf("operation %s cannot be presigned by aws-sdk-go-v2 (no Presign%s method)", name, name)
	}
	in, body, err := BuildInput(name, params, resolve)
	if err != nil {
		return nil, nil, err
	}
	args := []reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(in)}
	if expires > 0 {
		args = append(args, reflect.ValueOf(s3.WithPresignExpires(expires)))
	}
	rets := m.Call(args)
	if errv := rets[1]; !errv.IsNil() {
		return nil, nil, fmt.Errorf("presigning %s: %w", name, errv.Interface().(error))
	}
	req, ok := rets[0].Interface().(*v4.PresignedHTTPRequest)
	if !ok {
		return nil, nil, fmt.Errorf("Presign%s returned unexpected type %T", name, rets[0].Interface())
	}
	return req, body, nil
}
