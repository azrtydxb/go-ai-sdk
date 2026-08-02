// Package bedrock implements the go-ai-sdk provider.LanguageModel
// interfaces against Amazon Bedrock's Converse and ConverseStream APIs,
// signing requests with AWS Signature Version 4.
package bedrock

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/sigv4"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName      = "bedrock"
	defaultRegion     = "us-east-1"
	defaultServiceOpt = "bedrock"
)

// Provider is an Amazon Bedrock-backed provider.LanguageModel factory.
type Provider struct {
	region     string
	baseURL    string
	creds      sigv4.Credentials
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithRegion sets the AWS region (default env AWS_REGION, else
// AWS_DEFAULT_REGION, else "us-east-1").
func WithRegion(region string) Option {
	return func(p *Provider) { p.region = region }
}

// WithCredentials sets the AWS credentials used to sign requests. Defaults
// to AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN.
func WithCredentials(accessKeyID, secretAccessKey, sessionToken string) Option {
	return func(p *Provider) {
		p.creds = sigv4.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    sessionToken,
		}
	}
}

// WithBaseURL overrides the API base URL (default
// "https://bedrock-runtime.{region}.amazonaws.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Amazon Bedrock Provider.
func New(opts ...Option) *Provider {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = defaultRegion
	}

	p := &Provider{
		region: region,
		creds: sigv4.Credentials{
			AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
			SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		},
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.baseURL == "" {
		p.baseURL = "https://bedrock-runtime." + p.region + ".amazonaws.com"
	}
	return p
}

// Model returns a provider.LanguageModel for the given Bedrock model ID
// (e.g. "anthropic.claude-3-sonnet-20240229-v1:0").
func (p *Provider) Model(id string) provider.LanguageModel {
	return &languageModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}
