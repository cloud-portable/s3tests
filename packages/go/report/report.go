// Package report holds the run metadata shared by the result reporters, per
// the corpus's reporting guide
// (https://github.com/cloud-portable/s3vectors/blob/main/docs/reporting.md).
// Each reporter lives in its own subpackage: report/junit (JUnit XML) and
// report/gotest (t.Run subtests) today; CTRF and TAP planned.
//
// Reporters consume the iter.Seq[VectorResult] that Runner.Run returns (or a
// collected slice via slices.Values), so a run can stream straight into a
// report file:
//
//	f, _ := os.Create("results.xml")
//	err := junit.Write(f, runner.Run(ctx, filter), report.Meta{
//		CorpusVersion: runner.CorpusVersion(),
//		Target:        "MinIO RELEASE.2026-07-01",
//		GeneratedAt:   time.Now(),
//	})
package report

import "time"

// Meta carries run-level metadata stamped into reports so results stay
// comparable across runs, runners and targets.
type Meta struct {
	// CorpusVersion is the vector corpus snapshot the run used (from
	// Runner.CorpusVersion()). Emitted as the "corpusVersion" property.
	CorpusVersion string
	// Target is a human-readable identifier of the server under test, e.g.
	// "MinIO RELEASE.2026-07-01". Emitted as the "target" property when set.
	Target string
	// Properties are extra run-level properties (emitted in sorted key
	// order).
	Properties map[string]string
	// GeneratedAt, when set, is stamped into reports as the generation time
	// (rendered in UTC). It is deliberately caller-supplied — reporters never
	// call time.Now() — so output stays a pure function of its inputs; leave
	// it zero to omit.
	GeneratedAt time.Time
	// OmitSkipped drops Outcome==Skipped vectors from the report. The
	// default (false) is faithful to the reporting guide and keeps result
	// sets comparable, but a narrowly filtered run reports mostly skips.
	OmitSkipped bool
}
