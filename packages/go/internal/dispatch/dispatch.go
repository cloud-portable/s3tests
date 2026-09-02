// Package dispatch executes $operation steps by reflecting onto the
// aws-sdk-go-v2 S3 client: it decodes vector params (AWS API-model member
// names) into SDK input structs, invokes the named operation, converts the
// output struct into a generic JSON-like value for response matching and
// capture-path evaluation, and extracts {status, code, message} from errors.
package dispatch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/cloud-portable/s3tests/packages/go/internal/match"
)

// Result is the observed outcome of one operation call.
type Result struct {
	Status int         // raw HTTP status (0 if the request never completed)
	Header http.Header // raw response headers
	Output any         // generic API-model response value (nil when Err != nil)
	Body   []byte      // drained streaming output body (e.g. GetObject), if any
	Err    error       // the SDK error, if the call failed
	Code   string      // S3 error code extracted from Err
	Msg    string      // S3 error message extracted from Err
}

var clientType = reflect.TypeFor[*s3.Client]()

// Supported reports whether the operation exists on *s3.Client (e.g. the
// long-deprecated PutBucketLifecycle does not).
func Supported(name string) bool {
	_, ok := clientType.MethodByName(name)
	return ok
}

// InputType returns the SDK input struct type for an operation.
func InputType(name string) (reflect.Type, error) {
	m, ok := clientType.MethodByName(name)
	if !ok {
		return nil, fmt.Errorf("operation %s is not supported by aws-sdk-go-v2 service/s3", name)
	}
	// Signature: func(c *Client, ctx context.Context, in *XInput, optFns ...func(*Options))
	return m.Type.In(2).Elem(), nil
}

// BuildInput decodes interpolated vector params into a new SDK input struct,
// returning the *XInput value and, when a Body param was set, its raw bytes
// (needed by the presign path, which sends the body itself).
func BuildInput(name string, params map[string]json.RawMessage, resolve match.ContentResolver) (any, []byte, error) {
	t, err := InputType(name)
	if err != nil {
		return nil, nil, err
	}
	in := reflect.New(t)
	d := &decoder{resolve: resolve}
	for key, raw := range params {
		f := in.Elem().FieldByName(key)
		if !f.IsValid() {
			return nil, nil, fmt.Errorf("operation %s has no parameter %q", name, key)
		}
		val, err := match.Decode(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("parameter %s.%s: %w", name, key, err)
		}
		if err := d.value(f, val, key); err != nil {
			return nil, nil, fmt.Errorf("parameter %s.%s: %w", name, key, err)
		}
	}
	return in.Interface(), d.body, nil
}

var (
	readerType = reflect.TypeFor[io.Reader]()
	timeType   = reflect.TypeFor[time.Time]()
)

// decoder converts decoded vector JSON into SDK input values, handling the
// shapes encoding/json cannot: streaming bodies, unions, timestamps in the
// corpus's several formats, and content descriptors in string params.
type decoder struct {
	resolve match.ContentResolver
	body    []byte // bytes behind an io.Reader Body param, if one was set
}

func (d *decoder) value(f reflect.Value, j any, name string) error {
	t := f.Type()
	switch {
	case t == readerType:
		// Streaming body: the value is a content descriptor.
		b, err := match.ContentValue(j, d.resolve)
		if err != nil {
			return err
		}
		f.Set(reflect.ValueOf(bytes.NewReader(b)))
		d.body = b
		return nil
	case t == timeType:
		s, ok := j.(string)
		if !ok {
			return fmt.Errorf("timestamp must be a string, got %T", j)
		}
		tm, err := parseTime(s)
		if err != nil {
			return err
		}
		f.Set(reflect.ValueOf(tm))
		return nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		if j == nil {
			return nil
		}
		p := reflect.New(t.Elem())
		if err := d.value(p.Elem(), j, name); err != nil {
			return err
		}
		f.Set(p)
		return nil
	case reflect.String: // includes enums (named string types)
		switch s := j.(type) {
		case string:
			f.SetString(s)
			return nil
		case json.Number:
			// e.g. PartNumberMarker: modeled *string, written as a number.
			f.SetString(s.String())
			return nil
		case map[string]any:
			return d.objectIntoString(f, s, name)
		}
		return fmt.Errorf("cannot decode %T into string", j)
	case reflect.Bool:
		b, ok := j.(bool)
		if !ok {
			return fmt.Errorf("cannot decode %T into bool", j)
		}
		f.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := j.(json.Number)
		if !ok {
			return fmt.Errorf("cannot decode %T into integer", j)
		}
		i, err := n.Int64()
		if err != nil {
			return err
		}
		f.SetInt(i)
		return nil
	case reflect.Float32, reflect.Float64:
		n, ok := j.(json.Number)
		if !ok {
			return fmt.Errorf("cannot decode %T into float", j)
		}
		fl, err := n.Float64()
		if err != nil {
			return err
		}
		f.SetFloat(fl)
		return nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			s, ok := j.(string)
			if !ok {
				return fmt.Errorf("cannot decode %T into []byte", j)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return err
			}
			f.SetBytes(b)
			return nil
		}
		arr, ok := j.([]any)
		if !ok {
			return fmt.Errorf("cannot decode %T into slice", j)
		}
		out := reflect.MakeSlice(t, len(arr), len(arr))
		for i, e := range arr {
			if err := d.value(out.Index(i), e, name); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		f.Set(out)
		return nil
	case reflect.Map:
		obj, ok := j.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot decode %T into map", j)
		}
		out := reflect.MakeMapWithSize(t, len(obj))
		for k, e := range obj {
			ev := reflect.New(t.Elem()).Elem()
			if err := d.value(ev, e, name); err != nil {
				return fmt.Errorf("[%s]: %w", k, err)
			}
			out.SetMapIndex(reflect.ValueOf(k).Convert(t.Key()), ev)
		}
		f.Set(out)
		return nil
	case reflect.Struct:
		obj, ok := j.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot decode %T into %s", j, t)
		}
		for k, e := range obj {
			ff := f.FieldByName(k)
			if !ff.IsValid() {
				return fmt.Errorf("%s has no member %q", t, k)
			}
			if err := d.value(ff, e, k); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
		return nil
	case reflect.Interface:
		members, ok := unionMembers[t]
		if !ok {
			return fmt.Errorf("cannot decode into interface %s", t)
		}
		obj, ok := j.(map[string]any)
		if !ok || len(obj) != 1 {
			return fmt.Errorf("union %s wants an object with exactly one member", t)
		}
		for k, e := range obj {
			mt, ok := members[k]
			if !ok {
				return fmt.Errorf("union %s has no member %q", t, k)
			}
			mv := reflect.New(mt)
			if err := d.value(mv.Elem().FieldByName("Value"), e, k); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			f.Set(mv)
		}
		return nil
	default:
		return fmt.Errorf("unsupported SDK field kind %s", t.Kind())
	}
}

// objectIntoString handles the two object-valued string params in the corpus:
// boto3-style CopySource ({Bucket, Key[, VersionId]}) and binary content
// descriptors ({"$base64": ...}) for SSE-C keys, whose wire form is base64.
func (d *decoder) objectIntoString(f reflect.Value, obj map[string]any, name string) error {
	if name == "CopySource" {
		bucket, _ := obj["Bucket"].(string)
		key, _ := obj["Key"].(string)
		if bucket != "" && key != "" {
			src := bucket + "/" + key
			if vid, _ := obj["VersionId"].(string); vid != "" {
				src += "?versionId=" + vid
			}
			f.SetString(src)
			return nil
		}
	}
	if b, err := match.ContentValue(obj, d.resolve); err == nil {
		f.SetString(base64.StdEncoding.EncodeToString(b))
		return nil
	}
	return fmt.Errorf("cannot decode object into string param %s", name)
}

// parseTime accepts the corpus's timestamp shapes: RFC3339 (the norm),
// HTTP-date ("Expires" params) and bare dates (lifecycle rules).
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, http.TimeFormat, "2006-01-02", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}

// Call executes one operation. A returned error is a *runner* problem
// (unsupported operation, undecodable params); server-side failures are
// reported inside Result.
func Call(ctx context.Context, client *s3.Client, name string, params map[string]json.RawMessage, resolve match.ContentResolver) (*Result, error) {
	m := reflect.ValueOf(client).MethodByName(name)
	if !m.IsValid() {
		return nil, fmt.Errorf("operation %s is not supported by aws-sdk-go-v2 service/s3", name)
	}
	in, _, err := BuildInput(name, params, resolve)
	if err != nil {
		return nil, err
	}
	var rc rawCapture
	optFn := func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, rc.register)
	}
	rets := m.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(in),
		reflect.ValueOf(optFn),
	})
	res := &Result{Status: rc.status, Header: rc.header}
	if errv := rets[1]; !errv.IsNil() {
		res.Err = errv.Interface().(error)
		res.Code, res.Msg = apiError(res.Err)
		return res, nil
	}
	res.Output = walkOutput(rets[0], &res.Body)
	return res, nil
}

// rawCapture records the wire status/headers via a deserialize middleware,
// which sees the response on success AND error paths.
type rawCapture struct {
	status int
	header http.Header
}

func (rc *rawCapture) register(stack *middleware.Stack) error {
	return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc("s3testsRawCapture",
		func(ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler) (middleware.DeserializeOutput, middleware.Metadata, error) {
			out, md, err := next.HandleDeserialize(ctx, in)
			if resp, ok := out.RawResponse.(*smithyhttp.Response); ok && resp != nil {
				rc.status = resp.StatusCode
				rc.header = resp.Header.Clone()
			}
			return out, md, err
		}), middleware.After)
}

func apiError(err error) (code, msg string) {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode(), ae.ErrorMessage()
	}
	return "", err.Error()
}

// walkOutput converts an SDK output struct into a generic JSON-like value:
// maps/slices/strings/int64/float64/bool. Nil pointers are omitted (so
// {"$absent": true} works), time.Time renders as RFC3339, streaming bodies
// are drained into *body and excluded.
func walkOutput(v reflect.Value, body *[]byte) any {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return walkOutput(v.Elem(), body)
	case reflect.Struct:
		if v.Type() == timeType {
			return v.Interface().(time.Time).UTC().Format(time.RFC3339Nano)
		}
		out := map[string]any{}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || f.Name == "ResultMetadata" {
				continue
			}
			fv := v.Field(i)
			if fv.Kind() == reflect.Interface && !fv.IsNil() && fv.Type().Implements(readerType) {
				if r, ok := fv.Interface().(io.Reader); ok {
					b, _ := io.ReadAll(r)
					if c, ok := fv.Interface().(io.Closer); ok {
						c.Close()
					}
					*body = b
					continue
				}
			}
			if (fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface ||
				fv.Kind() == reflect.Slice || fv.Kind() == reflect.Map) && fv.IsNil() {
				continue // absent
			}
			out[f.Name] = walkOutput(fv, body)
		}
		return out
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return base64.StdEncoding.EncodeToString(v.Bytes())
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = walkOutput(v.Index(i), body)
		}
		return out
	case reflect.Map:
		out := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = walkOutput(iter.Value(), body)
		}
		return out
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	default:
		return fmt.Sprint(v.Interface())
	}
}
