// Command htmlreport runs the compatibility corpus against an S3 endpoint
// and writes the HTML report. It is the example consumer of the s3tests
// library: select vectors with filters, stream Run's results straight into a
// reporter.
//
//	go run ./examples/htmlreport -target "MinIO (local docker)" -o report.html
//	go run ./examples/htmlreport -tags tier-1 -o report-tier-1.html
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	s3tests "github.com/cloud-portable/s3tests/packages/go"
	"github.com/cloud-portable/s3tests/packages/go/report"
	"github.com/cloud-portable/s3tests/packages/go/report/html"
)

func main() {
	endpoint := flag.String("endpoint", "http://127.0.0.1:9000", "S3 endpoint under test")
	accessKey := flag.String("access-key", "minioadmin", "access key id")
	secretKey := flag.String("secret-key", "minioadmin", "secret access key")
	target := flag.String("target", "", "human-readable target name shown in the report (defaults to the endpoint)")
	groups := flag.String("groups", "", "comma-separated feature groups to run (empty = all)")
	tags := flag.String("tags", "", "comma-separated tags; vectors must carry at least one (e.g. tier-1)")
	concurrency := flag.Int("concurrency", 4, "vectors executed in parallel")
	out := flag.String("o", "report.html", "output file")
	flag.Parse()

	if err := run(*endpoint, *accessKey, *secretKey, *target, *groups, *tags, *concurrency, *out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(endpoint, accessKey, secretKey, target, groups, tags string, concurrency int, out string) error {
	runner, err := s3tests.New(s3tests.Config{
		Endpoint:    endpoint,
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		Concurrency: concurrency,
	})
	if err != nil {
		return err
	}

	vectors, err := s3tests.Vectors()
	if err != nil {
		return err
	}
	var filters []s3tests.FilterFunc
	properties := map[string]string{}
	if groups != "" {
		filters = append(filters, s3tests.Groups(strings.Split(groups, ",")...))
		properties["groups"] = groups
	}
	if tags != "" {
		filters = append(filters, s3tests.Tags(strings.Split(tags, ",")...))
		properties["tags"] = tags
	}
	selected := s3tests.ApplyFilters(vectors, filters...)
	if len(selected) == 0 {
		return fmt.Errorf("no vectors selected")
	}

	if target == "" {
		target = endpoint
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := html.Write(f, runner.Run(context.Background(), selected), report.Meta{
		CorpusVersion: runner.CorpusVersion(),
		Target:        target,
		Properties:    properties,
		GeneratedAt:   time.Now(),
	}); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d vectors, corpus %s)\n", out, len(selected), runner.CorpusVersion())
	return nil
}
