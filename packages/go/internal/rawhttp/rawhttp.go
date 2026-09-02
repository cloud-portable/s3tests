// Package rawhttp executes $http steps over a raw TCP/TLS connection. The
// corpus's wire-level tests send headers net/http refuses to emit (empty
// authorization, content-length "" or "-1"), so requests are serialized by
// hand and responses read with http.ReadResponse.
package rawhttp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Header is one wire header. Order is preserved as given.
type Header struct{ Name, Value string }

// Request describes a raw request before signing and default-header assembly.
type Request struct {
	Method  string
	Path    string              // interpolated raw path, starts with "/"
	Query   map[string][]string // interpolated query values (sent in sorted-key order)
	Headers []Header            // step headers in declaration order: applied as overrides
	Body    []byte

	// Sign controls SigV4 signing with Creds/Region. When false the request
	// is sent byte-literal with no auth headers at all.
	Sign   bool
	Creds  aws.Credentials
	Region string
}

// Response is the observed raw response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do assembles, optionally signs, sends the request to endpoint
// ("http(s)://host[:port]") and reads the response.
func Do(ctx context.Context, endpoint string, req *Request) (*Response, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("bad endpoint %q: %w", endpoint, err)
	}
	host := u.Host
	target := req.Path
	if q := encodeQuery(req.Query); q != "" {
		target += "?" + q
	}

	// Defaults, later overridden by step headers (case-insensitively; an
	// override applies even with an empty value — that is the point of the
	// wire-header tests).
	headers := []Header{
		{"Host", host},
		{"Content-Length", strconv.Itoa(len(req.Body))},
		{"Connection", "close"},
	}
	if req.Sign {
		signed, err := signHeaders(ctx, req, host, target)
		if err != nil {
			return nil, err
		}
		headers = append(headers, signed...)
	}
	headers = applyOverrides(headers, req.Headers)

	res, err := send(ctx, u, req.Method, target, headers, req.Body)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func encodeQuery(q map[string][]string) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			// Values are sent as given: these are wire-level tests and the
			// corpus's query values need no escaping.
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "&")
}

// signHeaders SigV4-signs a shadow request carrying the step headers (so the
// signature covers them) and returns the resulting auth headers.
// Content-Length is deliberately not signed: SigV4 does not require it, and
// the corpus overrides it on requests that must still authenticate.
func signHeaders(ctx context.Context, req *Request, host, target string) ([]Header, error) {
	rawPath, rawQuery, _ := strings.Cut(target, "?")
	shadow := &http.Request{
		Method: req.Method,
		// The v4 signer documents URL.Opaque ("//host/path") as the way to
		// sign an un-normalized request path.
		URL:    &url.URL{Opaque: "//" + host + rawPath, RawQuery: rawQuery},
		Host:   host,
		Header: http.Header{},
	}
	payloadHash := hex.EncodeToString(func() []byte { s := sha256.Sum256(req.Body); return s[:] }())
	for _, h := range req.Headers {
		if strings.EqualFold(h.Name, "content-length") {
			continue // wire-only; never signed
		}
		if strings.EqualFold(h.Name, "x-amz-content-sha256") {
			payloadHash = h.Value
		}
		shadow.Header.Set(h.Name, h.Value)
	}
	shadow.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signer := v4.NewSigner(func(o *v4.SignerOptions) {
		o.DisableURIPathEscaping = true // S3: paths are canonicalized as-is
	})
	if err := signer.SignHTTP(ctx, req.Creds, shadow, payloadHash, "s3", req.Region, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("signing request: %w", err)
	}
	var out []Header
	for _, name := range []string{"X-Amz-Date", "X-Amz-Content-Sha256", "X-Amz-Security-Token", "Authorization"} {
		if v := shadow.Header.Get(name); v != "" {
			out = append(out, Header{name, v})
		}
	}
	return out, nil
}

func applyOverrides(base []Header, overrides []Header) []Header {
	out := base
	for _, o := range overrides {
		replaced := false
		for i := range out {
			if strings.EqualFold(out[i].Name, o.Name) {
				out[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

func send(ctx context.Context, endpoint *url.URL, method, target string, headers []Header, body []byte) (*Response, error) {
	addr := endpoint.Host
	if endpoint.Port() == "" {
		if endpoint.Scheme == "https" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if endpoint.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: endpoint.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		conn = tc
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", method, target)
	for _, h := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", h.Name, h.Value)
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return nil, fmt.Errorf("writing request: %w", err)
	}
	if len(body) > 0 {
		if _, err := conn.Write(body); err != nil {
			return nil, fmt.Errorf("writing body: %w", err)
		}
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: respBody}, nil
}

// ParseXMLError extracts <Error><Code>/<Message> from an S3 XML error body.
// Returns empty strings when the body is not an XML error document.
func ParseXMLError(body []byte) (code, message string) {
	var e struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &e); err != nil {
		return "", ""
	}
	return e.Code, e.Message
}
