// Package s3tests is a programmatic runner for the language-independent S3
// compatibility test vectors published by
// github.com/cloud-portable/s3vectors. It executes `api`-kind vectors
// against an S3 endpoint — provisioning prerequisites, interpolating
// placeholders, dispatching operations through aws-sdk-go-v2, sending raw
// wire-level requests for $http steps, and evaluating expectations — and
// reports an outcome per vector: pass, fail, blocked or skipped.
//
// Minimal usage:
//
//	runner, err := s3tests.New(s3tests.Config{
//		Endpoint:    "http://127.0.0.1:9000",
//		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
//	})
//	vectors, err := s3tests.Vectors()
//	selected := s3tests.ApplyFilters(vectors, s3tests.Groups("object-crud"), s3tests.Tags("tier-1"))
//	for res := range runner.Run(ctx, selected) {
//		fmt.Println(res.Outcome, res.ID)
//	}
//
// Run executes exactly the vectors it is given: ApplyFilters composes the
// built-in group/tag/id filters with any custom FilterFunc, but any reduction
// of the Vectors() slice works. Vectors dropped this way leave no trace in
// the results; to record vectors as skipped instead — keeping reports
// comparable across runs — pass Skip / SkipFunc options to Run:
//
//	runner.Run(ctx, selected, s3tests.Skip("known bug #123", s3tests.IDs("multipart-0013")))
//
// Signing-kind vectors (offline SigV4 algorithm tests) are out of scope and
// never loaded.
package s3tests

import (
	"context"
	"iter"
	"sync"

	s3vectors "github.com/alanshaw/s3vectors/packages/go"
)

// Vectors returns every api-kind vector in the corpus, in manifest order.
// The vectors point into the shared, cached corpus — treat them as read-only.
func Vectors() ([]*s3vectors.Vector, error) {
	files, err := s3vectors.All()
	if err != nil {
		return nil, err
	}
	var out []*s3vectors.Vector
	for _, f := range files {
		for i := range f.Vectors {
			if v := &f.Vectors[i]; v.IsAPI() {
				out = append(out, v)
			}
		}
	}
	return out, nil
}

// Runner executes vectors against one endpoint. Construct with New; a Runner
// is safe for one Run at a time.
type Runner struct {
	cfg    Config
	ids    *identities
	target Target
}

// New validates the configuration and prepares a Runner.
func New(cfg Config) (*Runner, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	r := &Runner{cfg: c}
	r.ids = newIdentities(&r.cfg)
	mainClient := r.ids.buildClient(c.Credentials)
	r.ids.clients[identityMain] = mainClient
	r.ids.creds[identityMain] = c.Credentials
	r.target = Target{Endpoint: c.Endpoint, Region: c.Region, Client: mainClient}
	return r, nil
}

// CorpusVersion identifies the vector corpus snapshot this runner executes.
// Stamp it into any emitted test report so results are comparable across
// runs, runners and targets.
func (r *Runner) CorpusVersion() string {
	return s3vectors.Manifest().Version
}

// Run executes the given vectors and streams one VectorResult per vector in
// completion order (identical to the given order when Concurrency is 1).
// Selection happens before Run — see Vectors and ApplyFilters. Vectors
// matched by a Skip / SkipFunc option are not executed but still yield a
// result with Outcome Skipped and the option's reason.
//
// Breaking out of the loop, or cancelling ctx, stops the run: in-flight
// vectors are cancelled (their resource teardown still runs) and
// not-yet-started vectors never run — a stopped stream is therefore
// incomplete. The iterator does not return until all in-flight work has
// wound down.
func (r *Runner) Run(ctx context.Context, vectors []*s3vectors.Vector, opts ...RunOption) iter.Seq[VectorResult] {
	var o runOptions
	for _, opt := range opts {
		opt(&o)
	}
	return func(yield func(VectorResult) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		indexes := make(chan int)
		results := make(chan VectorResult)

		var wg sync.WaitGroup
		for w := 0; w < r.cfg.Concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range indexes {
					if ctx.Err() != nil {
						continue // cancelled before this vector started
					}
					var res VectorResult
					if reason, skip := o.skipReason(vectors[idx]); skip {
						res = newResult(vectors[idx])
						res.Outcome, res.Reason = Skipped, reason
					} else {
						res = r.runVector(ctx, vectors[idx])
					}
					select {
					case results <- res:
					case <-ctx.Done():
						// The consumer is gone; the vector's teardown has
						// already run inside runVector.
					}
				}
			}()
		}
		go func() {
			defer close(indexes)
			for idx := range vectors {
				select {
				case indexes <- idx:
				case <-ctx.Done():
					return
				}
			}
		}()
		go func() {
			wg.Wait()
			close(results)
		}()

		for res := range results {
			if !yield(res) {
				cancel()
				// Wait for in-flight vectors (and their teardowns) to finish
				// so returning from the loop means the run has fully stopped.
				for range results {
				}
				return
			}
		}
	}
}
