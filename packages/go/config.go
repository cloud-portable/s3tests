package s3tests

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config configures a Runner.
type Config struct {
	// Endpoint of the server under test, "http(s)://host[:port]". Required.
	Endpoint string
	// Region for SigV4 signing. Defaults to "us-east-1".
	Region string
	// Credentials is the primary ("main") identity. Required.
	Credentials aws.CredentialsProvider

	// HTTPClient is used by the SDK clients and for executing presigned
	// requests. Defaults to a plain &http.Client{}.
	HTTPClient *http.Client
	// VirtualHostStyle switches to virtual-hosted-style addressing; the
	// default is path-style, which most non-AWS targets expect.
	VirtualHostStyle bool
	// Concurrency is the number of vectors executed in parallel. Defaults
	// to 1 (deterministic, debuggable). Steps within a vector are always
	// sequential.
	Concurrency int
	// BucketPrefix prefixes every runner-created bucket name so leftovers
	// are identifiable. Defaults to "s3tests-".
	BucketPrefix string

	// ProvisionCredential supplies the second identity for $credential
	// prerequisites (handle "alt" throughout the corpus). Creating accounts
	// is server-specific, so there is no default: when nil, vectors needing
	// one report Blocked.
	ProvisionCredential func(ctx context.Context, handle string) (Credential, error)

	// Provisioner establishes $bucket and $object prerequisites and tears
	// vectors' resources down. Defaults to DefaultProvisioner, which uses
	// the endpoint itself (CreateBucket/PutObject).
	Provisioner Provisioner
	// KeepResources skips teardown, leaving buckets in place for debugging.
	KeepResources bool
}

// Credential is a full second identity as provisioned for $credential
// prerequisites. CanonicalID and DisplayName feed the corresponding
// ${res.<handle>.*} placeholders (used heavily by ACL vectors).
type Credential struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	CanonicalID     string
	DisplayName     string
}

// Target hands provisioners the runner's execution context.
type Target struct {
	Endpoint string
	Region   string
	// Client is a ready-made main-identity S3 client configured like the
	// runner's own (path style, no retries, no implicit checksums).
	Client *s3.Client
}

const (
	identityMain      = "main"
	identityAnonymous = "anonymous"
	identityInvalid   = "invalid"
)

func (c *Config) withDefaults() (Config, error) {
	out := *c
	if out.Endpoint == "" {
		return out, fmt.Errorf("s3tests: Config.Endpoint is required")
	}
	if out.Credentials == nil {
		return out, fmt.Errorf("s3tests: Config.Credentials is required")
	}
	if out.Region == "" {
		out.Region = "us-east-1"
	}
	if out.HTTPClient == nil {
		out.HTTPClient = &http.Client{}
	}
	if out.Concurrency < 1 {
		out.Concurrency = 1
	}
	if out.BucketPrefix == "" {
		out.BucketPrefix = "s3tests-"
	}
	if out.Provisioner == nil {
		out.Provisioner = DefaultProvisioner{}
	}
	return out, nil
}

// identities lazily builds and caches per-identity S3 clients and raw
// credentials for the run.
type identities struct {
	cfg *Config

	mu      sync.Mutex
	clients map[string]*s3.Client
	presign map[string]*s3.PresignClient
	creds   map[string]aws.CredentialsProvider
	alt     map[string]*altEntry
}

type altEntry struct {
	once sync.Once
	cred Credential
	err  error
}

func newIdentities(cfg *Config) *identities {
	return &identities{
		cfg:     cfg,
		clients: map[string]*s3.Client{},
		presign: map[string]*s3.PresignClient{},
		creds:   map[string]aws.CredentialsProvider{},
		alt:     map[string]*altEntry{},
	}
}

// buildClient constructs an S3 client tuned for compatibility testing: the
// vectors own their wire bytes (no implicit checksums, no retries, no
// Expect: 100-continue) and expectations need the first response, verbatim.
func (ids *identities) buildClient(provider aws.CredentialsProvider) *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint:               aws.String(ids.cfg.Endpoint),
		Region:                     ids.cfg.Region,
		UsePathStyle:               !ids.cfg.VirtualHostStyle,
		Credentials:                provider,
		Retryer:                    aws.NopRetryer{},
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		DisableLogOutputChecksumValidationSkipped: true,
		ContinueHeaderThresholdBytes:              -1,
		HTTPClient:                                ids.cfg.HTTPClient,
	})
}

// provider returns the credentials provider for an identity, provisioning
// credential-handle identities on first use (once per run).
func (ids *identities) provider(ctx context.Context, identity string) (aws.CredentialsProvider, error) {
	ids.mu.Lock()
	if p, ok := ids.creds[identity]; ok {
		ids.mu.Unlock()
		return p, nil
	}
	ids.mu.Unlock()

	var p aws.CredentialsProvider
	switch identity {
	case identityMain:
		p = ids.cfg.Credentials
	case identityAnonymous:
		p = aws.AnonymousCredentials{}
	case identityInvalid:
		// Well-formed signature under an access key the server cannot know.
		p = credentials.NewStaticCredentialsProvider(
			"AKIAS3TESTSINVALID00", "s3tests-invalid-secret-key-0000000000000", "")
	default:
		cred, err := ids.provisionAlt(ctx, identity)
		if err != nil {
			return nil, err
		}
		p = credentials.NewStaticCredentialsProvider(cred.AccessKeyID, cred.SecretAccessKey, cred.SessionToken)
	}
	ids.mu.Lock()
	ids.creds[identity] = p
	ids.mu.Unlock()
	return p, nil
}

// credential returns the provisioned Credential for a $credential handle
// (used both for clients and for ${res.<handle>.*} attributes).
func (ids *identities) provisionAlt(ctx context.Context, handle string) (Credential, error) {
	if ids.cfg.ProvisionCredential == nil {
		return Credential{}, fmt.Errorf("no ProvisionCredential configured (required for $credential prerequisite %q)", handle)
	}
	ids.mu.Lock()
	e, ok := ids.alt[handle]
	if !ok {
		e = &altEntry{}
		ids.alt[handle] = e
	}
	ids.mu.Unlock()
	e.once.Do(func() {
		e.cred, e.err = ids.cfg.ProvisionCredential(ctx, handle)
	})
	return e.cred, e.err
}

// client returns the (cached) S3 client for an identity.
func (ids *identities) client(ctx context.Context, identity string) (*s3.Client, error) {
	ids.mu.Lock()
	if c, ok := ids.clients[identity]; ok {
		ids.mu.Unlock()
		return c, nil
	}
	ids.mu.Unlock()
	p, err := ids.provider(ctx, identity)
	if err != nil {
		return nil, err
	}
	c := ids.buildClient(p)
	ids.mu.Lock()
	ids.clients[identity] = c
	ids.mu.Unlock()
	return c, nil
}

// presignClient returns the (cached) presign client for an identity.
func (ids *identities) presignClient(ctx context.Context, identity string) (*s3.PresignClient, error) {
	ids.mu.Lock()
	if pc, ok := ids.presign[identity]; ok {
		ids.mu.Unlock()
		return pc, nil
	}
	ids.mu.Unlock()
	c, err := ids.client(ctx, identity)
	if err != nil {
		return nil, err
	}
	pc := s3.NewPresignClient(c)
	ids.mu.Lock()
	ids.presign[identity] = pc
	ids.mu.Unlock()
	return pc, nil
}

// retrieve returns raw credentials for an identity ($http signing).
func (ids *identities) retrieve(ctx context.Context, identity string) (aws.Credentials, error) {
	p, err := ids.provider(ctx, identity)
	if err != nil {
		return aws.Credentials{}, err
	}
	return p.Retrieve(ctx)
}
