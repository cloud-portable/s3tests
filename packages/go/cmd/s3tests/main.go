// Command s3tests runs the S3 compatibility test-vector corpus against an
// endpoint and reports the results.
//
// By default results just stream to the console. File reports are written
// for each --report (-r) flag, given as <format> (default path: report.xml
// for junit, report.html for html) or <format>=<path>, repeatable:
//
//	s3tests -endpoint http://127.0.0.1:9000 -access-key AK -secret-key SK \
//	  -tags tier-1 -r junit -r html=minio.html
//
// The exit code is 1 when any vector failed (including runner errors) and 0
// otherwise; blocked vectors do not affect it (a missing second identity
// blocks the $credential vectors by design — supply -alt-access-key /
// -alt-secret-key to run them).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"iter"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	s3tests "github.com/cloud-portable/s3tests/packages/go"
	"github.com/cloud-portable/s3tests/packages/go/report"
	"github.com/cloud-portable/s3tests/packages/go/report/html"
	"github.com/cloud-portable/s3tests/packages/go/report/junit"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("s3tests", flag.ContinueOnError)
	fs.SetOutput(stderr)

	endpoint := fs.String("endpoint", envOr("S3TESTS_ENDPOINT", ""), "S3 endpoint under test, http(s)://host[:port] (env S3TESTS_ENDPOINT)")
	accessKey := fs.String("access-key", envOr("S3TESTS_ACCESS_KEY", ""), "access key id (env S3TESTS_ACCESS_KEY)")
	secretKey := fs.String("secret-key", envOr("S3TESTS_SECRET_KEY", ""), "secret access key (env S3TESTS_SECRET_KEY)")
	region := fs.String("region", "us-east-1", "region for SigV4 signing")
	virtualHost := fs.Bool("virtual-host", false, "use virtual-hosted-style addressing (default path-style)")
	concurrency := fs.Int("concurrency", 1, "vectors executed in parallel")
	keep := fs.Bool("keep-resources", false, "skip teardown, leaving buckets in place for debugging")

	altAccessKey := fs.String("alt-access-key", envOr("S3TESTS_ALT_ACCESS_KEY", ""), "second identity access key for $credential vectors (env S3TESTS_ALT_ACCESS_KEY)")
	altSecretKey := fs.String("alt-secret-key", envOr("S3TESTS_ALT_SECRET_KEY", ""), "second identity secret key (env S3TESTS_ALT_SECRET_KEY)")
	altCanonicalID := fs.String("alt-canonical-id", envOr("S3TESTS_ALT_CANONICAL_ID", ""), "second identity canonical id (for ACL vectors)")
	altDisplayName := fs.String("alt-display-name", envOr("S3TESTS_ALT_DISPLAY_NAME", ""), "second identity display name")

	groups := fs.String("groups", "", "comma-separated feature groups to run (empty = all)")
	tags := fs.String("tags", "", "comma-separated tags; vectors must carry at least one (e.g. tier-1)")
	ids := fs.String("ids", "", "comma-separated vector ids to run")
	excludeGroups := fs.String("exclude-groups", "", "comma-separated feature groups to skip")
	excludeTags := fs.String("exclude-tags", "", "comma-separated tags to skip")
	excludeIDs := fs.String("exclude-ids", "", "comma-separated vector ids to skip (skip-list)")

	var reports reportFlags
	fs.Var(&reports, "report", "write a report, <format>[=<path>] (formats: junit, html; default paths report.xml, report.html); repeatable")
	fs.Var(&reports, "r", "shorthand for -report")
	target := fs.String("target", "", "human-readable target name stamped into reports (defaults to the endpoint)")
	quiet := fs.Bool("quiet", false, "suppress per-vector progress output")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" || *accessKey == "" || *secretKey == "" {
		fmt.Fprintln(stderr, "error: -endpoint, -access-key and -secret-key are required")
		fs.Usage()
		return 2
	}

	cfg := s3tests.Config{
		Endpoint:         *endpoint,
		Region:           *region,
		Credentials:      credentials.NewStaticCredentialsProvider(*accessKey, *secretKey, ""),
		VirtualHostStyle: *virtualHost,
		Concurrency:      *concurrency,
		KeepResources:    *keep,
	}
	if *altAccessKey != "" && *altSecretKey != "" {
		cred := s3tests.Credential{
			AccessKeyID:     *altAccessKey,
			SecretAccessKey: *altSecretKey,
			CanonicalID:     *altCanonicalID,
			DisplayName:     *altDisplayName,
		}
		cfg.ProvisionCredential = func(context.Context, string) (s3tests.Credential, error) {
			return cred, nil
		}
	}
	runner, err := s3tests.New(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	vectors, err := s3tests.Vectors()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	filters, properties := buildFilters(*groups, *tags, *ids, *excludeGroups, *excludeTags, *excludeIDs)
	selected := s3tests.ApplyFilters(vectors, filters...)
	if len(selected) == 0 {
		fmt.Fprintln(stderr, "error: no vectors selected")
		return 2
	}

	// Ctrl-C cancels the run; in-flight vectors still tear their buckets down.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	color := colorsEnabled(stdout)
	counts := map[s3tests.Outcome]int{}
	runnerErrs := 0
	started := time.Now()
	var results []s3tests.VectorResult
	for res := range runner.Run(ctx, selected) {
		results = append(results, res)
		counts[res.Outcome]++
		if res.RunnerError != "" {
			runnerErrs++
		}
		if !*quiet {
			fmt.Fprintln(stdout, progressLine(res, color))
		}
	}
	wall := time.Since(started)
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(stderr, "run interrupted:", err)
	}

	meta := report.Meta{
		CorpusVersion: runner.CorpusVersion(),
		Target:        *target,
		Properties:    properties,
		GeneratedAt:   time.Now(),
	}
	if meta.Target == "" {
		meta.Target = *endpoint
	}
	reporterFailed := false
	for _, spec := range reports {
		f, err := os.Create(spec.path)
		if err == nil {
			err = reporters[spec.format].write(f, slices.Values(results), meta)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			fmt.Fprintf(stderr, "error: writing %s report: %v\n", spec.format, err)
			reporterFailed = true
			continue
		}
		fmt.Fprintf(stdout, "wrote %s report %s\n", spec.format, spec.path)
	}

	pass, fail, blocked := counts[s3tests.Pass], counts[s3tests.Fail], counts[s3tests.Blocked]
	attempted := len(results)
	pct := "—"
	if attempted > 0 {
		pct = fmt.Sprintf("%.1f%%", 100*float64(pass)/float64(attempted))
	}
	fmt.Fprintf(stdout, "\n%d vectors: %d pass, %d fail (%d runner errors), %d blocked — %s pass rate in %.1fs (corpus %s)\n",
		attempted, pass, fail, runnerErrs, blocked, pct, wall.Seconds(), runner.CorpusVersion())

	switch {
	case fail > 0 || reporterFailed:
		return 1
	case ctx.Err() != nil:
		return 130
	default:
		return 0
	}
}

// reporters maps --report format names onto the report subpackages, with the
// path used when the flag gives only a format name.
var reporters = map[string]struct {
	write       func(io.Writer, iter.Seq[s3tests.VectorResult], report.Meta) error
	defaultPath string
}{
	"junit": {junit.Write, "report.xml"},
	"html":  {html.Write, "report.html"},
}

func reporterFormats() string {
	names := make([]string, 0, len(reporters))
	for name := range reporters {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// reportFlags collects repeatable --report/-r <format>=<path> values.
type reportFlags []reportSpec

type reportSpec struct{ format, path string }

func (r *reportFlags) String() string {
	parts := make([]string, len(*r))
	for i, spec := range *r {
		parts[i] = spec.format + "=" + spec.path
	}
	return strings.Join(parts, ",")
}

func (r *reportFlags) Set(v string) error {
	format, path, hasPath := strings.Cut(v, "=")
	if format == "" || (hasPath && path == "") {
		return fmt.Errorf("expected <format> or <format>=<path>, got %q", v)
	}
	reporter, known := reporters[format]
	if !known {
		return fmt.Errorf("unknown report format %q (formats: %s)", format, reporterFormats())
	}
	if !hasPath {
		path = reporter.defaultPath
	}
	*r = append(*r, reportSpec{format: format, path: path})
	return nil
}

// buildFilters turns the selection flags into filter funcs, plus the
// properties stamped into reports so filtered runs self-describe.
func buildFilters(groups, tags, ids, exGroups, exTags, exIDs string) ([]s3tests.FilterFunc, map[string]string) {
	var filters []s3tests.FilterFunc
	properties := map[string]string{}
	add := func(name, val string, f func(...string) s3tests.FilterFunc) {
		if val == "" {
			return
		}
		filters = append(filters, f(strings.Split(val, ",")...))
		properties[name] = val
	}
	add("groups", groups, s3tests.Groups)
	add("tags", tags, s3tests.Tags)
	add("ids", ids, s3tests.IDs)
	add("exclude-groups", exGroups, s3tests.ExcludeGroups)
	add("exclude-tags", exTags, s3tests.ExcludeTags)
	add("exclude-ids", exIDs, s3tests.ExcludeIDs)
	return filters, properties
}

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiAmber  = "\x1b[33m"
	ansiViolet = "\x1b[35m"
)

// colorsEnabled reports whether stdout is a terminal and NO_COLOR is unset.
func colorsEnabled(stdout io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := stdout.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// progressLine renders one vector result for the console.
func progressLine(res s3tests.VectorResult, color bool) string {
	outcome, tint := string(res.Outcome), ""
	detail := ""
	switch res.Outcome {
	case s3tests.Pass:
		tint = ansiGreen
	case s3tests.Fail:
		tint = ansiRed
		if res.RunnerError != "" {
			outcome, tint = "error", ansiViolet
			detail = " — runner error: " + res.RunnerError
		} else if len(res.Steps) > 0 {
			step := res.Steps[len(res.Steps)-1]
			detail = fmt.Sprintf(" — step %d (%s) failed", step.Index+1, step.Name)
			if len(step.Failures) > 0 {
				f := step.Failures[0]
				detail += fmt.Sprintf(": %s: expected %s, got %s", f.Field, f.Expected, f.Actual)
			} else if step.Err != "" {
				detail += ": " + step.Err
			}
		}
	case s3tests.Blocked:
		tint = ansiAmber
		detail = " — " + res.Reason
	}
	if color && tint != "" {
		outcome = tint + outcome + ansiReset
	}
	return fmt.Sprintf("%8s %s (%.2fs)%s", outcome, res.ID, res.Duration.Seconds(), detail)
}
