package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// fail500 answers every request with a 500 XML error: vectors with
// prerequisites block (provisioning fails), vectors without prerequisites
// fail their first step.
func fail500() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `<?xml version="1.0"?><Error><Code>Boom</Code><Message>nope</Message></Error>`)
	}))
}

// pickVector returns the id of the first api vector matching the predicate.
func pickVector(t *testing.T, want func(*s3vectors.Vector) bool) string {
	t.Helper()
	vectors, err := s3tests.Vectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		if want(v) {
			return v.ID
		}
	}
	t.Fatal("no matching vector in corpus")
	return ""
}

func TestCLIFailuresExitOne(t *testing.T) {
	srv := fail500()
	defer srv.Close()
	id := pickVector(t, func(v *s3vectors.Vector) bool { return len(v.Prerequisites) == 0 })

	dir := t.TempDir()
	junitPath := filepath.Join(dir, "report.xml")
	htmlPath := filepath.Join(dir, "report.html")
	var out, errOut bytes.Buffer
	code := run([]string{
		"-endpoint", srv.URL, "-access-key", "AK", "-secret-key", "SK",
		"-ids", id,
		"-r", "junit=" + junitPath, "--report", "html=" + htmlPath,
	}, &out, &errOut)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	for _, want := range []string{id, "1 fail", "wrote junit report", "wrote html report"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}
	// Both reporters produced files containing the vector.
	for _, p := range []string{junitPath, htmlPath} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("report not written: %v", err)
		}
		if !strings.Contains(string(b), id) {
			t.Errorf("%s does not mention %s", p, id)
		}
	}
}

func TestCLIBlockedExitsZero(t *testing.T) {
	srv := fail500()
	defer srv.Close()
	id := pickVector(t, func(v *s3vectors.Vector) bool {
		return len(v.Prerequisites) > 0 && v.Prerequisites[0].Bucket != nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{
		"-endpoint", srv.URL, "-access-key", "AK", "-secret-key", "SK",
		"-ids", id, "-quiet",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (blocked is not failure)\nstdout:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "1 blocked") {
		t.Errorf("summary missing blocked count:\n%s", out.String())
	}
	if strings.Contains(out.String(), "blocked "+id) {
		t.Error("-quiet must suppress progress lines")
	}
}

func TestCLIUsageErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-access-key", "AK"}, &out, &errOut); code != 2 {
		t.Errorf("missing endpoint: exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "-endpoint") {
		t.Errorf("usage error not explained:\n%s", errOut.String())
	}
	var out2, errOut2 bytes.Buffer
	if code := run([]string{"-endpoint", "http://x", "-access-key", "AK", "-secret-key", "SK", "-ids", "no-such-0001"}, &out2, &errOut2); code != 2 {
		t.Errorf("empty selection: exit %d, want 2", code)
	}
}

func TestCLIReportFlag(t *testing.T) {
	base := []string{"-endpoint", "http://x", "-access-key", "AK", "-secret-key", "SK"}
	for _, bad := range []string{"tap=report.tap", "tap", "junit=", "=x"} {
		var out, errOut bytes.Buffer
		if code := run(append(base, "-r", bad), &out, &errOut); code != 2 {
			t.Errorf("-r %q: exit %d, want 2", bad, code)
		}
	}
	var out, errOut bytes.Buffer
	run(append(base, "-r", "tap=x"), &out, &errOut)
	if !strings.Contains(errOut.String(), "formats: html, junit") {
		t.Errorf("unknown format must list known formats:\n%s", errOut.String())
	}
}

// A bare format name writes to its default path in the working directory.
func TestCLIReportDefaultPaths(t *testing.T) {
	srv := fail500()
	defer srv.Close()
	id := pickVector(t, func(v *s3vectors.Vector) bool { return len(v.Prerequisites) == 0 })

	t.Chdir(t.TempDir())
	var out, errOut bytes.Buffer
	run([]string{
		"-endpoint", srv.URL, "-access-key", "AK", "-secret-key", "SK",
		"-ids", id, "-quiet", "-r", "junit", "-r", "html",
	}, &out, &errOut)

	for _, p := range []string{"report.xml", "report.html"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("default-path report missing: %v", err)
		}
		if !strings.Contains(out.String(), "report "+p) {
			t.Errorf("stdout missing write notice for %s:\n%s", p, out.String())
		}
	}
}
