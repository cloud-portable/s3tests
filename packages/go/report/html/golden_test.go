package html

// The golden test is the cross-implementation drift alarm: every runner
// implementation renders shared/report/fixture.json and must produce
// shared/report/golden.html byte-for-byte. The Go implementation is
// canonical — regenerate with `make golden` after intentional template or
// view-model changes, then verify the other implementations still match.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	s3tests "github.com/alanshaw/s3tests/packages/go"
	"github.com/alanshaw/s3tests/packages/go/report"
)

const sharedDir = "../../../../shared/report"

type fixture struct {
	Meta    report.Meta            `json:"meta"`
	Results []s3tests.VectorResult `json:"results"`
}

func TestGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(sharedDir, "fixture.json"))
	if err != nil {
		t.Fatalf("shared fixture missing: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var buf bytes.Buffer
	if err := Write(&buf, slices.Values(f.Results), f.Meta); err != nil {
		t.Fatal(err)
	}

	goldenPath := filepath.Join(sharedDir, "golden.html")
	if os.Getenv("S3TESTS_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden updated: %s (%d bytes)", goldenPath, buf.Len())
		return
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden missing (run `make golden`): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), golden) {
		t.Fatalf("rendered report differs from shared golden (%d vs %d bytes) — if the change is intentional, run `make golden` and re-verify every implementation", buf.Len(), len(golden))
	}
}
