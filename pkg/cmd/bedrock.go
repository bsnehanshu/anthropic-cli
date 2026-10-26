package cmd

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/anthropics/anthropic-sdk-go/v2/bedrock"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/urfave/cli/v3"
)

// One-shot guard for the stderr notice below. Reassigned to a fresh sync.Once
// by tests via a reset helper.
var amazonBedrockBaseURLNoticeOnce sync.Once

// amazonBedrock holds the inputs that address Claude in Amazon Bedrock: the
// Messages API endpoint Bedrock serves at bedrock-mantle.{region}.api.aws. The
// endpoint URL and the request signing are handled by the SDK's bedrock
// package; this type is only the CLI's flag/env carrier plus the "configured
// or not" classification used by `auth status` and the precedence switch in
// getDefaultRequestOptions.
type amazonBedrock struct {
	// Region picks the endpoint and the signing region. When empty the SDK
	// falls back to AWS_REGION, then AWS_DEFAULT_REGION.
	Region string
	// APIKey is a Bedrock API key from --bedrock-api-key-stdin or
	// AWS_BEARER_TOKEN_BEDROCK. When empty, requests are signed with AWS
	// credentials from the default credential chain.
	APIKey string
	// apiKeyFromStdin records that the key was piped to
	// --bedrock-api-key-stdin, which selects the tier where the environment
	// variable alone does not.
	apiKeyFromStdin bool
	// BaseURL replaces the regional endpoint URL. Like the Google Cloud and
	// AWS tiers, this tier never takes --base-url / ANTHROPIC_BASE_URL.
	BaseURL string
}

// amazonBedrockFromRoot builds an amazonBedrock from the root command's flag
// values, so a subcommand's same-named local flag can't shadow them.
// applyStdinCredential must run first for the API key.
func amazonBedrockFromRoot(root *cli.Command) amazonBedrock {
	apiKey, _ := directCredential("bedrock-api-key")
	return amazonBedrock{
		Region:          root.String("bedrock-region"),
		APIKey:          apiKey,
		apiKeyFromStdin: credentialFromStdin(root, "bedrock-api-key"),
		BaseURL:         root.String("bedrock-base-url"),
	}
}

// AnySet reports whether the user addressed Bedrock at all: a region or a key
// piped to --bedrock-api-key-stdin selects the tier. AWS_REGION and
// AWS_PROFILE on their own do not count; they are set on most AWS machines
// for unrelated reasons. Nor does AWS_BEARER_TOKEN_BEDROCK: other Bedrock
// clients read it too, so it is exported on machines whose `ant` is logged
// in elsewhere. Nor does the base URL, for the same reason as the Google
// Cloud tier.
func (b amazonBedrock) AnySet() bool {
	return b.Region != "" || b.apiKeyFromStdin
}

// apiKeySource names where the API key came from, without printing it.
func (b amazonBedrock) apiKeySource(root *cli.Command) string {
	if credentialFromStdin(root, "bedrock-api-key") {
		return "--bedrock-api-key-stdin"
	}
	return "AWS_BEARER_TOKEN_BEDROCK"
}

// host is where this tier sends requests, for display and for the origin
// `ant apply` records: the base URL override, or the regional endpoint. Empty
// when no region is set anywhere, since the SDK then has no endpoint to derive.
func (b amazonBedrock) host() string {
	region := cmp.Or(b.Region, os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	if b.BaseURL != "" || region == "" {
		return b.BaseURL
	}
	return fmt.Sprintf("https://bedrock-mantle.%s.api.aws/anthropic", region)
}

// noteIgnoredBaseURL tells the user, once, that the generic base URL is not
// applied, for the same reason as the Google Cloud tier's notice.
func (b amazonBedrock) noteIgnoredBaseURL(w io.Writer, baseURL string) {
	if baseURL == "" {
		return
	}
	amazonBedrockBaseURLNoticeOnce.Do(func() {
		fmt.Fprintf(w,
			"Note: --base-url / ANTHROPIC_BASE_URL is not used with Amazon Bedrock credentials; requests go to %s. To send them elsewhere: --bedrock-base-url / ANTHROPIC_BEDROCK_MANTLE_BASE_URL.\n",
			cmp.Or(b.host(), "the regional endpoint"))
	})
}

// requestOptions returns the request options for the Bedrock credential
// tier: the endpoint base URL and either the API key or a middleware that
// signs each request with AWS credentials.
func (b amazonBedrock) requestOptions(ctx context.Context) ([]option.RequestOption, error) {
	if err := ValidateBaseURL(b.BaseURL, "--bedrock-base-url / ANTHROPIC_BEDROCK_MANTLE_BASE_URL"); err != nil {
		return nil, err
	}
	client, err := bedrock.NewMantleClient(ctx, bedrock.MantleClientConfig{
		AWSRegion: b.Region,
		APIKey:    b.APIKey,
		BaseURL:   b.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("%w\n"+
			"  Credentials come from the AWS default credential chain (AWS_PROFILE, environment keys, an IAM role),\n"+
			"  or from a Bedrock API key in AWS_BEARER_TOKEN_BEDROCK / --bedrock-api-key-stdin.\n"+
			"  Region: --bedrock-region / ANTHROPIC_BEDROCK_REGION / AWS_REGION / AWS_DEFAULT_REGION", err)
	}
	return client.Options, nil
}
