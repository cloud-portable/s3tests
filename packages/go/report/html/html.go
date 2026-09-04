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
// The template is mustache, shared verbatim across runner implementations
// (canonical copy: shared/report/page.mustache, synced by
// scripts/sync-report-template.js). Every implementation builds the same
// JSON-shaped view model — sections switch only on booleans and arrays, and
// all numbers are pre-formatted with integer arithmetic — so identical
// results render byte-identical pages in every language, enforced by the
// shared golden test (shared/report/fixture.json + golden.html).
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
	"io"
	"iter"
	"sort"
	"strings"
	"time"

	"github.com/cbroglie/mustache"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
	"github.com/cloud-portable/s3tests/packages/go/report"
)

//go:embed page.mustache
var pageSrc string

var page = func() *mustache.Template {
	t, err := mustache.ParseString(pageSrc)
	if err != nil {
		panic("s3tests: embedded report template invalid: " + err.Error())
	}
	return t
}()

// Write consumes the results and writes the report page.
func Write(w io.Writer, results iter.Seq[s3tests.VectorResult], meta report.Meta) error {
	return page.FRender(w, viewModel(results, meta))
}

// viewModel builds the shared cross-implementation view model: a JSON-shaped
// map whose conditionals are explicit booleans and whose numbers are
// pre-formatted strings (see the package comment).
func viewModel(results iter.Seq[s3tests.VectorResult], meta report.Meta) map[string]any {
	type group struct {
		name    string
		counts  counts
		vectors []map[string]any
	}
	groupIdx := map[string]int{}
	var groups []*group
	var totals counts
	var totalTime time.Duration

	for res := range results {
		if meta.OmitSkipped && res.Outcome == s3tests.Skipped {
			continue
		}
		i, ok := groupIdx[res.Group]
		if !ok {
			i = len(groups)
			groupIdx[res.Group] = i
			groups = append(groups, &group{name: res.Group})
		}
		g := groups[i]
		g.counts.add(res)
		totals.add(res)
		totalTime += res.Duration
		g.vectors = append(g.vectors, vectorView(res))
	}

	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })

	firstFail := ""
	groupViews := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.vectors, func(i, j int) bool {
			return g.vectors[i]["id"].(string) < g.vectors[j]["id"].(string)
		})
		if firstFail == "" {
			for _, vec := range g.vectors {
				if vec["badge"] == "fail" {
					firstFail = vec["id"].(string)
					break
				}
			}
		}
		groupViews = append(groupViews, map[string]any{
			"name":     g.name,
			"pctClass": g.counts.pctClass(),
			"barWidth": g.counts.barWidth(),
			"open":     g.counts.Fail+g.counts.Errors > 0,
			"counts":   g.counts.view(),
			"vectors":  g.vectors,
		})
	}

	generated := ""
	if !meta.GeneratedAt.IsZero() {
		generated = meta.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	return map[string]any{
		"target":           meta.Target,
		"hasTarget":        meta.Target != "",
		"corpusVersion":    meta.CorpusVersion,
		"hasCorpusVersion": meta.CorpusVersion != "",
		"generated":        generated,
		"hasGenerated":     generated != "",
		"totalTime":        humanDuration(totalTime),
		"properties":       sortedProperties(meta.Properties),
		"hasProvenance":    meta.Target != "" || meta.CorpusVersion != "" || generated != "" || len(meta.Properties) > 0,
		"totals":           totals.view(),
		"firstFail":        firstFail,
		"groups":           groupViews,
	}
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

// attempted is the pass-rate denominator: everything except skipped.
func (c *counts) attempted() int { return c.Total - c.Skipped }

func (c *counts) view() map[string]any {
	return map[string]any{
		"pass": c.Pass, "fail": c.Fail, "blocked": c.Blocked,
		"errors": c.Errors, "skipped": c.Skipped, "total": c.Total,
		"attempted": c.attempted(),
		"passPct":   c.passPct(),
		"hasFail":   c.Fail > 0, "hasBlocked": c.Blocked > 0,
		"hasErrors": c.Errors > 0, "hasSkipped": c.Skipped > 0,
		"failZero": c.Fail == 0, "blockedZero": c.Blocked == 0,
		"errorsZero": c.Errors == 0, "skippedZero": c.Skipped == 0,
	}
}

// passPct renders the pass rate ("98.2%", one decimal, round half away from
// zero via integer arithmetic — the shared cross-implementation rule), or a
// dash when nothing was attempted.
func (c *counts) passPct() string {
	attempted := c.attempted()
	if attempted == 0 {
		return "—"
	}
	p10 := (1000*c.Pass + attempted/2) / attempted
	return fmt.Sprintf("%d.%d%%", p10/10, p10%10)
}

// pctClass maps the pass rate onto the coverage-style watermarks using exact
// integer comparisons (high >= 80%, medium >= 50%).
func (c *counts) pctClass() string {
	attempted := c.attempted()
	switch {
	case attempted == 0:
		return ""
	case 100*c.Pass >= 80*attempted:
		return "high"
	case 100*c.Pass >= 50*attempted:
		return "medium"
	default:
		return "low"
	}
}

// barWidth is the pass-fraction bar fill width in whole percent.
func (c *counts) barWidth() string {
	attempted := c.attempted()
	if attempted == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", (100*c.Pass+attempted/2)/attempted)
}

// definitionBase is the raw-file location of the corpus vector files; a
// text-fragment URL on it opens the file scrolled to the vector's "id" line.
const definitionBase = "https://raw.githubusercontent.com/cloud-portable/s3vectors/main/vectors/"

// definitionURL links a vector card to the vector's definition in the corpus
// repository. Group and id are plain concatenated, not percent-encoded: the
// corpus schema restricts both to [a-z0-9-], and avoiding an encoder keeps
// the Go and JS reporters byte-identical (their encoders escape different
// characters). The fragment prefix is the pre-encoded form of `"id": "`.
func definitionURL(group, id string) string {
	return definitionBase + group + ".json#:~:text=%22id%22%3A%20%22" + id + "%22"
}

func vectorView(res s3tests.VectorResult) map[string]any {
	badge := string(res.Outcome)
	reason, summary, detail := "", "", ""
	switch res.Outcome {
	case s3tests.Fail:
		summary, detail = failureDetail(res)
		if res.RunnerError != "" {
			badge = "error"
			summary = "runner error: " + res.RunnerError
			if detail == res.RunnerError {
				detail = "" // nothing beyond what the message already says
			}
		}
	case s3tests.Blocked:
		reason = "blocked: " + res.Reason
	case s3tests.Skipped:
		reason = "skipped: " + res.Reason
	}
	tags := res.Tags
	if tags == nil {
		tags = []string{}
	}
	warnings := res.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return map[string]any{
		"id": res.ID, "badge": badge,
		"duration": vectorDuration(res.Duration),
		"title":    res.Title, "hasTitle": res.Title != "",
		"tags": tags, "hasTags": len(tags) > 0,
		"reason": reason, "hasReason": reason != "",
		"summary": summary, "hasSummary": summary != "",
		"detail": detail, "hasDetail": detail != "",
		"warnings": warnings,
		"source":   res.Source, "hasSource": res.Source != "",
		"definitionURL": definitionURL(res.Group, res.ID),
		"hasDesc":       res.Title != "" || len(tags) > 0,
		"hasOutcome":    reason != "" || summary != "" || detail != "" || len(warnings) > 0,
	}
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

// vectorDuration renders a per-vector duration as seconds with exactly three
// decimals, from whole milliseconds (round half away from zero) — the shared
// cross-implementation rule.
func vectorDuration(d time.Duration) string {
	ms := (d.Nanoseconds() + 500_000) / 1_000_000
	return fmt.Sprintf("%d.%03ds", ms/1000, ms%1000)
}

// humanDuration renders a duration compactly: "42.3s" under a minute
// (one decimal, round half away from zero), Go-style "4m12s" above (rounded
// to whole seconds).
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		ds := (d.Nanoseconds() + 50_000_000) / 100_000_000
		return fmt.Sprintf("%d.%ds", ds/10, ds%10)
	}
	return d.Round(time.Second).String()
}

func sortedProperties(props map[string]string) []map[string]string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]string{"name": k, "value": props[k]})
	}
	return out
}
