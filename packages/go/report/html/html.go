// Package html renders runner results as a single self-contained HTML page
// for human inspection — no external assets, no JavaScript — with an
// aesthetic modeled on the istanbul/c8 coverage report: a summary strip of
// big numbers, a per-group table with pale high/medium/low row tinting and
// small pass-fraction bars, and per-group detail sections with
// expected-vs-actual failure detail behind native <details> disclosure.
//
// The headline pass rate is pass / (total − skipped): everything the runner
// attempted counts in the denominator (blocked vectors and runner errors
// included — their counts are displayed separately so the nuance stays
// visible).
//
// Output is byte-deterministic for identical input: no timestamps are
// stamped. Put a date in report.Meta.Properties if you want one shown.
package html

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"iter"
	"sort"
	"strings"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
	"github.com/cloud-portable/s3tests/packages/go/report"
)

//go:embed page.tmpl
var pageSrc string

var page = template.Must(template.New("page").Parse(pageSrc))

// Write consumes the results and writes the report page.
func Write(w io.Writer, results iter.Seq[s3tests.VectorResult], meta report.Meta) error {
	v := &pageView{
		CorpusVersion: meta.CorpusVersion,
		Target:        meta.Target,
		Properties:    sortedProperties(meta.Properties),
	}
	groupIdx := map[string]int{} // group -> index, first-encounter order

	for res := range results {
		if meta.OmitSkipped && res.Outcome == s3tests.Skipped {
			continue
		}
		i, ok := groupIdx[res.Group]
		if !ok {
			i = len(v.Groups)
			groupIdx[res.Group] = i
			v.Groups = append(v.Groups, &groupView{Name: res.Group})
		}
		g := v.Groups[i]
		g.Counts.add(res)
		v.Totals.add(res)
		g.Vectors = append(g.Vectors, vectorView(res))
	}

	for _, g := range v.Groups {
		g.Rate = g.Counts.rate()
	}
	v.Rate = v.Totals.rate()

	return page.Execute(w, v)
}

type pageView struct {
	CorpusVersion string
	Target        string
	Properties    []property
	Totals        counts
	Rate          rate
	Groups        []*groupView
}

type property struct{ Name, Value string }

type groupView struct {
	Name    string
	Counts  counts
	Rate    rate
	Vectors []vecView
}

type counts struct {
	Pass, Fail, Blocked, Errors, Skipped, Total int
}

func (c *counts) add(res s3tests.VectorResult) {
	c.Total++
	switch res.Outcome {
	case s3tests.Pass:
		c.Pass++
	case s3tests.Fail:
		if res.RunnerError != "" {
			c.Errors++
		} else {
			c.Fail++
		}
	case s3tests.Blocked:
		c.Blocked++
	case s3tests.Skipped:
		c.Skipped++
	}
}

// Attempted is the pass-rate denominator: everything except skipped.
func (c *counts) Attempted() int { return c.Total - c.Skipped }

// rate is a pass rate prepared for display: percentage text, bar width and
// the istanbul-style high/medium/low class.
type rate struct {
	Pct   string // "98.2%" or "—" when nothing was attempted
	Width string // bar fill width, "0"–"100"
	Class string // "high" | "medium" | "low" | ""
}

func (c *counts) rate() rate {
	attempted := c.Attempted()
	if attempted == 0 {
		return rate{Pct: "—", Width: "0"}
	}
	pct := 100 * float64(c.Pass) / float64(attempted)
	r := rate{
		Pct:   fmt.Sprintf("%.1f%%", pct),
		Width: fmt.Sprintf("%.0f", pct),
	}
	switch {
	case pct >= 80:
		r.Class = "high"
	case pct >= 50:
		r.Class = "medium"
	default:
		r.Class = "low"
	}
	return r
}

type vecView struct {
	ID       string
	Title    string
	Badge    string // pass | fail | blocked | error | skipped
	Duration string
	Reason   string // inline text (blocked/skipped reasons)
	Summary  string // <details> summary line for failures
	Detail   string // <details> body: expected/actual lines
	Warnings []string
}

func vectorView(res s3tests.VectorResult) vecView {
	v := vecView{
		ID:       res.ID,
		Title:    res.Title,
		Badge:    string(res.Outcome),
		Duration: fmt.Sprintf("%.3fs", res.Duration.Seconds()),
		Warnings: res.Warnings,
	}
	switch res.Outcome {
	case s3tests.Fail:
		v.Summary, v.Detail = failureDetail(res)
		if res.RunnerError != "" {
			v.Badge = "error"
			v.Summary = "runner error: " + res.RunnerError
		}
	case s3tests.Blocked:
		v.Reason = "blocked: " + res.Reason
	case s3tests.Skipped:
		v.Reason = "skipped: " + res.Reason
	}
	return v
}

// failureDetail summarizes the failing (last executed) step: a one-line
// summary plus every expected/actual mismatch for the disclosure body.
func failureDetail(res s3tests.VectorResult) (summary, detail string) {
	if len(res.Steps) == 0 {
		return res.Reason, ""
	}
	step := res.Steps[len(res.Steps)-1]
	prefix := fmt.Sprintf("step %d (%s)", step.Index+1, step.Name)
	var lines []string
	if step.Err != "" {
		lines = append(lines, step.Err)
	}
	for _, f := range step.Failures {
		lines = append(lines, fmt.Sprintf("%s: expected %s, got %s", f.Field, f.Expected, f.Actual))
	}
	if len(lines) == 0 {
		return prefix + ": failed", ""
	}
	return prefix + ": " + lines[0], strings.Join(lines, "\n")
}

func sortedProperties(props map[string]string) []property {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]property, 0, len(keys))
	for _, k := range keys {
		out = append(out, property{Name: k, Value: props[k]})
	}
	return out
}
