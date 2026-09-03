// Package html renders runner results as a single self-contained HTML page
// for human inspection — no external assets, no JavaScript. The look and
// feel is a dark-first compatibility dashboard (light theme via
// prefers-color-scheme): a header with an overall split badge of outcome
// counts, a coverage-style summary table of groups (pass-fraction bars,
// watermarked pass percentages, per-outcome counts), and a
// staged-disclosure section per group whose vector cards open to
// expected-vs-actual detail. Groups with failures start expanded (their
// vector cards stay collapsed until clicked); clean groups start collapsed.
// Navigation is anchors only: group names jump to their section and the
// fail badge jumps to the first failing vector.
//
// The headline pass rate is pass / (total − skipped): everything the runner
// attempted counts in the denominator (blocked vectors and runner errors
// included — their counts are displayed separately so the nuance stays
// visible).
//
// Output is byte-deterministic for identical input: the generation time is
// never sampled by the reporter itself — set report.Meta.GeneratedAt to
// stamp one (shown in the provenance panel and footer, in UTC).
package html

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"iter"
	"sort"
	"strings"
	"time"

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
	if !meta.GeneratedAt.IsZero() {
		v.Generated = meta.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	groupIdx := map[string]int{} // group -> index (sorted by name below)
	var totalTime time.Duration

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
		totalTime += res.Duration
		g.Vectors = append(g.Vectors, vectorView(res))
	}
	v.TotalTime = humanDuration(totalTime)

	// Results arrive in completion order, which interleaves under
	// concurrency: present groups by name and vectors by id (ids are
	// zero-padded, so lexical order is numeric order).
	sort.Slice(v.Groups, func(i, j int) bool { return v.Groups[i].Name < v.Groups[j].Name })
	for _, g := range v.Groups {
		sort.Slice(g.Vectors, func(i, j int) bool { return g.Vectors[i].ID < g.Vectors[j].ID })
		g.PctClass = g.Counts.pctClass()
		g.BarWidth = g.Counts.barWidth()
		g.Open = g.Counts.Fail+g.Counts.Errors > 0
		if v.FirstFail == "" {
			for _, vec := range g.Vectors {
				if vec.Badge == "fail" {
					v.FirstFail = vec.ID
					break
				}
			}
		}
	}

	return page.Execute(w, v)
}

type pageView struct {
	CorpusVersion string
	Target        string
	Generated     string // formatted Meta.GeneratedAt; "" omits it
	// TotalTime is the summed vector execution time — wall-clock run time
	// only when the runner's Concurrency is 1 (JUnit `time` semantics).
	TotalTime  string
	Properties []property
	Totals     counts
	FirstFail  string // id of the first failing vector, for the badge jump
	Groups     []*groupView
}

type property struct{ Name, Value string }

type groupView struct {
	Name     string
	Counts   counts
	PctClass string // high | medium | low ("" when nothing attempted)
	BarWidth string // pass-fraction bar fill width, "0"–"100"
	Open     bool   // groups with failures start expanded
	Vectors  []vecView
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

// PassPct renders the pass rate ("98.2%"), or a dash when nothing was
// attempted.
func (c *counts) PassPct() string {
	attempted := c.Attempted()
	if attempted == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(c.Pass)/float64(attempted))
}

// pctClass maps the pass rate onto the coverage-style watermarks.
func (c *counts) pctClass() string {
	attempted := c.Attempted()
	if attempted == 0 {
		return ""
	}
	switch pct := 100 * float64(c.Pass) / float64(attempted); {
	case pct >= 80:
		return "high"
	case pct >= 50:
		return "medium"
	default:
		return "low"
	}
}

// barWidth is the pass-fraction bar fill width in percent.
func (c *counts) barWidth() string {
	attempted := c.Attempted()
	if attempted == 0 {
		return "0"
	}
	return fmt.Sprintf("%.0f", 100*float64(c.Pass)/float64(attempted))
}

type vecView struct {
	ID       string
	Title    string
	Tags     []string
	Badge    string // pass | fail | blocked | error | skipped
	Duration string
	Reason   string // blocked/skipped reasons
	Summary  string // failing step one-liner
	Detail   string // expected/actual lines
	Source   string
	Warnings []string
}

func vectorView(res s3tests.VectorResult) vecView {
	v := vecView{
		ID:       res.ID,
		Title:    res.Title,
		Tags:     res.Tags,
		Badge:    string(res.Outcome),
		Duration: fmt.Sprintf("%.3fs", res.Duration.Seconds()),
		Source:   res.Source,
		Warnings: res.Warnings,
	}
	switch res.Outcome {
	case s3tests.Fail:
		v.Summary, v.Detail = failureDetail(res)
		if res.RunnerError != "" {
			v.Badge = "error"
			v.Summary = "runner error: " + res.RunnerError
			if v.Detail == res.RunnerError {
				v.Detail = "" // nothing beyond what the message already says
			}
		}
	case s3tests.Blocked:
		v.Reason = "blocked: " + res.Reason
	case s3tests.Skipped:
		v.Reason = "skipped: " + res.Reason
	}
	return v
}

// failureDetail summarizes the failing (last executed) step. The summary
// names only the step — the mismatches live solely in the detail block, so
// nothing renders twice.
func failureDetail(res s3tests.VectorResult) (summary, detail string) {
	if len(res.Steps) == 0 {
		return res.Reason, ""
	}
	step := res.Steps[len(res.Steps)-1]
	summary = fmt.Sprintf("step %d (%s) failed", step.Index+1, step.Name)
	var lines []string
	if step.Err != "" {
		lines = append(lines, step.Err)
	}
	for _, f := range step.Failures {
		lines = append(lines, fmt.Sprintf("%s: expected %s, got %s", f.Field, f.Expected, f.Actual))
	}
	return summary, strings.Join(lines, "\n")
}

// humanDuration renders a duration compactly: "42.3s" under a minute,
// "4m12s" style above.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
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
