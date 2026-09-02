package rawhttp

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// rawServer accepts one connection, captures the raw request bytes up to the
// end of headers (plus any body by content-length when parseable), and writes
// a fixed response.
func rawServer(t *testing.T, response string) (addr string, got chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		var b strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			b.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		got <- b.String()
		conn.Write([]byte(response))
	}()
	return l.Addr().String(), got
}

const okResponse = "HTTP/1.1 200 OK\r\nx-amz-request-id: R1\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"

func TestUnsignedLiteralBytes(t *testing.T) {
	addr, got := rawServer(t, okResponse)
	res, err := Do(context.Background(), "http://"+addr, &Request{
		Method: "PUT",
		Path:   "/bucket/k",
		Query:  map[string][]string{"partNumber": {"1"}},
		Headers: []Header{
			{"content-length", "-1"},
			{"x-weird", ""},
		},
		Body: []byte("abc"),
		Sign: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.Header.Get("x-amz-request-id") != "R1" || string(res.Body) != "ok" {
		t.Errorf("response: %+v body %q", res, res.Body)
	}
	raw := <-got
	if !strings.HasPrefix(raw, "PUT /bucket/k?partNumber=1 HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", raw)
	}
	if !strings.Contains(raw, "content-length: -1\r\n") {
		t.Errorf("content-length override not sent literally:\n%s", raw)
	}
	if !strings.Contains(raw, "x-weird: \r\n") {
		t.Errorf("empty header value not sent:\n%s", raw)
	}
	if strings.Contains(strings.ToLower(raw), "authorization") || strings.Contains(strings.ToLower(raw), "x-amz-date") {
		t.Errorf("unsigned request must carry no auth headers:\n%s", raw)
	}
}

func TestSignedRequest(t *testing.T) {
	addr, got := rawServer(t, okResponse)
	res, err := Do(context.Background(), "http://"+addr, &Request{
		Method:  "PUT",
		Path:    "/bucket/key",
		Headers: []Header{{"content-md5", "rL0Y20xC+Fzt72VPzMSk2A=="}},
		Body:    []byte("da"),
		Sign:    true,
		Creds:   aws.Credentials{AccessKeyID: "AK", SecretAccessKey: "SK"},
		Region:  "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 {
		t.Errorf("status %d", res.Status)
	}
	raw := <-got
	if !strings.Contains(raw, "Authorization: AWS4-HMAC-SHA256 Credential=AK/") {
		t.Errorf("missing/typo Authorization:\n%s", raw)
	}
	// The signature must cover the step's content-md5 header but never
	// content-length (which the corpus overrides on signed requests).
	lower := strings.ToLower(raw)
	authLine := lower[strings.Index(lower, "authorization:"):]
	authLine = authLine[:strings.Index(authLine, "\r\n")]
	if !strings.Contains(authLine, "content-md5") {
		t.Errorf("content-md5 not in SignedHeaders: %s", authLine)
	}
	if strings.Contains(authLine, "content-length") {
		t.Errorf("content-length must not be signed: %s", authLine)
	}
	if !strings.Contains(raw, "X-Amz-Content-Sha256: ") || !strings.Contains(raw, "X-Amz-Date: ") {
		t.Errorf("missing sigv4 headers:\n%s", raw)
	}
	if !strings.Contains(raw, "Content-Length: 2\r\n") {
		t.Errorf("default content-length missing:\n%s", raw)
	}
}

func TestAuthorizationOverrideWins(t *testing.T) {
	addr, got := rawServer(t, okResponse)
	_, err := Do(context.Background(), "http://"+addr, &Request{
		Method:  "GET",
		Path:    "/bucket",
		Headers: []Header{{"authorization", ""}},
		Sign:    true,
		Creds:   aws.Credentials{AccessKeyID: "AK", SecretAccessKey: "SK"},
		Region:  "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := <-got
	if !strings.Contains(raw, "authorization: \r\n") {
		t.Errorf("authorization override must win over the signed value:\n%s", raw)
	}
	if strings.Contains(raw, "AWS4-HMAC-SHA256") {
		t.Errorf("signed authorization leaked despite override:\n%s", raw)
	}
}

func TestParseXMLError(t *testing.T) {
	code, msg := ParseXMLError([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`))
	if code != "NoSuchKey" || msg != "nope" {
		t.Errorf("got %q %q", code, msg)
	}
	if code, _ := ParseXMLError([]byte("not xml")); code != "" {
		t.Errorf("non-xml should give empty code, got %q", code)
	}
	if code, _ := ParseXMLError(nil); code != "" {
		t.Errorf("empty body should give empty code, got %q", code)
	}
}
