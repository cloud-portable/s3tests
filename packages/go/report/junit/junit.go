// Package junit renders runner results as JUnit XML, the de facto
// interchange format for test results (rendered natively by GitHub Actions,
// GitLab and Jenkins).
package junit

import (
	"encoding/xml"
	"fmt"
	"io"
	"iter"
	"sort"
	"strconv"
	"strings"
	"time"

	s3tests "github.com/cloud-portable/s3tests/packages/go"
	"github.com/cloud-portable/s3tests/packages/go/report"
)

// Write consumes the results and writes a JUnit XML document: one <testcase>
// per vector (name = vector id, classname = area), one <testsuite> per area.
// Mapping per the corpus reporting guide:
//
//   - pass    -> testcase with no child element
//   - fail    -> <failure> with per-check expected/actual detail; vectors the
//     runner could not execute (VectorResult.RunnerError) become <error>
//     instead, so compat failures and runner problems stay distinguishable
//   - blocked -> <skipped message="blocked: ...">  (never <failure>)
//   - skipped -> <skipped message="skipped: ...">  (dropped by Meta.OmitSkipped)
//
// Meta fields become suite-level <properties>; vector tags become a per-case
// "tags" property.
func Write(w io.Writer, results iter.Seq[s3tests.VectorResult], meta report.Meta) error {
	root := junitTestsuites{Name: "s3vectors"}
	suiteIdx := map[string]int{} // area -> index into root.Suites, first-encounter order
	var totalTime time.Duration

	for res := range results {
		if meta.OmitSkipped && res.Outcome == s3tests.Skipped {
			continue
		}
		i, ok := suiteIdx[res.Group]
		if !ok {
			i = len(root.Suites)
			suiteIdx[res.Group] = i
			root.Suites = append(root.Suites, junitTestsuite{
				Name:       res.Group,
				Properties: suiteProperties(meta),
			})
		}
		suite := &root.Suites[i]
		suite.Cases = append(suite.Cases, newTestcase(res))
		suite.Tests++
		root.Tests++
		switch res.Outcome {
		case s3tests.Fail:
			if res.RunnerError != "" {
				suite.Errors++
				root.Errors++
			} else {
				suite.Failures++
				root.Failures++
			}
		case s3tests.Blocked, s3tests.Skipped:
			suite.Skipped++
			root.Skipped++
		}
		suite.time += res.Duration
		totalTime += res.Duration
	}

	for i := range root.Suites {
		root.Suites[i].Time = seconds(root.Suites[i].time)
	}
	root.Time = seconds(totalTime)

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func newTestcase(res s3tests.VectorResult) junitTestcase {
	tc := junitTestcase{
		Name:      res.ID,
		Classname: res.Group,
		Time:      seconds(res.Duration),
	}
	if len(res.Tags) > 0 {
		tc.Properties = &junitProperties{Property: []junitProperty{
			{Name: "tags", Value: strings.Join(res.Tags, ",")},
		}}
	}
	switch res.Outcome {
	case s3tests.Fail:
		msg, body := failureDetail(res)
		if res.RunnerError != "" {
			tc.Error = &junitResult{Message: "runner error: " + res.RunnerError, Body: body}
		} else {
			tc.Failure = &junitResult{Message: msg, Body: body}
		}
	case s3tests.Blocked:
		tc.Skipped = &junitMessage{Message: "blocked: " + res.Reason}
	case s3tests.Skipped:
		tc.Skipped = &junitMessage{Message: "skipped: " + res.Reason}
	}
	if len(res.Warnings) > 0 {
		tc.SystemOut = strings.Join(res.Warnings, "\n")
	}
	return tc
}

// failureDetail summarizes the failing (last executed) step: a one-line
// message for the attribute plus the full expected/actual detail as text.
func failureDetail(res s3tests.VectorResult) (msg, body string) {
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

func suiteProperties(meta report.Meta) *junitProperties {
	var props []junitProperty
	if meta.CorpusVersion != "" {
		props = append(props, junitProperty{Name: "corpusVersion", Value: meta.CorpusVersion})
	}
	if meta.Target != "" {
		props = append(props, junitProperty{Name: "target", Value: meta.Target})
	}
	keys := make([]string, 0, len(meta.Properties))
	for k := range meta.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		props = append(props, junitProperty{Name: k, Value: meta.Properties[k]})
	}
	if len(props) == 0 {
		return nil
	}
	return &junitProperties{Property: props}
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

type junitTestsuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name       string           `xml:"name,attr"`
	Tests      int              `xml:"tests,attr"`
	Failures   int              `xml:"failures,attr"`
	Errors     int              `xml:"errors,attr"`
	Skipped    int              `xml:"skipped,attr"`
	Time       string           `xml:"time,attr"`
	Properties *junitProperties `xml:"properties"`
	Cases      []junitTestcase  `xml:"testcase"`

	time time.Duration
}

type junitProperties struct {
	Property []junitProperty `xml:"property"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitTestcase struct {
	Name       string           `xml:"name,attr"`
	Classname  string           `xml:"classname,attr"`
	Time       string           `xml:"time,attr"`
	Properties *junitProperties `xml:"properties"`
	Skipped    *junitMessage    `xml:"skipped"`
	Failure    *junitResult     `xml:"failure"`
	Error      *junitResult     `xml:"error"`
	SystemOut  string           `xml:"system-out,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
}

type junitResult struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}
