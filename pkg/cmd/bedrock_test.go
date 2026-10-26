package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-cli/v2/internal/declarative/core"
	"github.com/anthropics/anthropic-sdk-go/v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const fakeBedrockAPIKey = "fake-bedrock-api-key"

// The Bedrock tier shares its test root, leaf runner and environment reset
// with the AWS tier: awsTestRoot carries both tiers' flags, runAWSLeaf
// captures the signed request, and setupAWSTest clears both tiers' variables.

// assertBedrockSigV4Request checks the request went to the regional Bedrock
// endpoint, signed for the bedrock-mantle service with the fake key, with no
// Anthropic credential and no workspace header.
func assertBedrockSigV4Request(t *testing.T, req *http.Request, region string) {
	t.Helper()
	assert.Equal(t, "https", req.URL.Scheme)
	assert.Equal(t, "bedrock-mantle."+region+".api.aws", req.URL.Host)
	assert.Equal(t, "/anthropic/v1/models", req.URL.Path)
	assert.Empty(t, req.Header.Values("anthropic-workspace-id"))
	assert.NotEmpty(t, req.Header.Get("X-Amz-Date"))
	assert.Empty(t, req.Header.Get("x-api-key"))

	m := sigV4Authorization.FindStringSubmatch(req.Header.Get("Authorization"))
	require.NotNil(t, m, "Authorization %q", req.Header.Get("Authorization"))
	assert.Equal(t, fakeAWSAccessKey, m[1])
	assert.Equal(t, region, m[2])
	assert.Equal(t, "bedrock-mantle", m[3])
	signed := strings.Split(m[4], ";")
	assert.Contains(t, signed, "host")
	assert.Contains(t, signed, "x-amz-date")
}

// TestBedrockTier pins when the Bedrock credential tier is selected and what
// it sends: the regional endpoint URL and either a SigV4 signature or the
// Bedrock API key.
func TestBedrockTier(t *testing.T) {
	t.Run("--bedrock-region selects the tier and signs with the AWS credential chain", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		req := runAWSLeaf(t, "--bedrock-region", "us-east-1")
		assertBedrockSigV4Request(t, req, "us-east-1")
		assert.Empty(t, req.Header.Get("X-Amz-Security-Token"))
	})

	t.Run("ANTHROPIC_BEDROCK_REGION selects the tier", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		t.Setenv("ANTHROPIC_BEDROCK_REGION", "ap-northeast-1")
		assertBedrockSigV4Request(t, runAWSLeaf(t), "ap-northeast-1")
	})

	t.Run("a piped key selects the tier; the region comes from AWS_DEFAULT_REGION", func(t *testing.T) {
		setupAWSTest(t)
		t.Setenv("AWS_DEFAULT_REGION", "eu-west-1")
		withStdinCredential(t, fakeBedrockAPIKey+"\n")
		req := runAWSLeaf(t, "--bedrock-api-key-stdin")
		assert.Equal(t, "bedrock-mantle.eu-west-1.api.aws", req.URL.Host)
		assert.Equal(t, "/anthropic/v1/models", req.URL.Path)
		assert.Equal(t, "Bearer "+fakeBedrockAPIKey, req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("x-api-key"))
	})

	t.Run("temporary credentials send the session token", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, fakeAWSSessionToken)
		req := runAWSLeaf(t, "--bedrock-region", "us-east-1")
		assertBedrockSigV4Request(t, req, "us-east-1")
		assert.Equal(t, fakeAWSSessionToken, req.Header.Get("X-Amz-Security-Token"))
	})

	t.Run("AWS_BEARER_TOKEN_BEDROCK sends the key instead of signing", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", fakeBedrockAPIKey)
		req := runAWSLeaf(t, "--bedrock-region", "us-east-1")
		assert.Equal(t, "bedrock-mantle.us-east-1.api.aws", req.URL.Host)
		assert.Equal(t, "Bearer "+fakeBedrockAPIKey, req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("x-api-key"))
	})

	t.Run("ANTHROPIC_CUSTOM_HEADERS is not sent", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Custom: one\nAuthorization: Bearer from-headers")
		req := runAWSLeaf(t, "--bedrock-region", "us-east-1")
		assertBedrockSigV4Request(t, req, "us-east-1")
		assert.Empty(t, req.Header.Get("X-Custom"))
	})

	t.Run("--bedrock-base-url replaces the endpoint", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		req := runAWSLeaf(t, "--bedrock-region", "us-east-1", "--bedrock-base-url", "https://endpoint.invalid")
		assert.Equal(t, "endpoint.invalid", req.URL.Host)
		assert.True(t, strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256 "))
	})

	t.Run("--base-url never receives AWS credentials", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		t.Setenv("ANTHROPIC_BASE_URL", "https://anthropic-proxy.invalid")
		var req *http.Request
		stderr := captureStderr(t, func() {
			req = runAWSLeaf(t, "--bedrock-region", "us-east-1")
		})
		assertBedrockSigV4Request(t, req, "us-east-1")
		assert.Contains(t, stderr, "--base-url / ANTHROPIC_BASE_URL is not used with Amazon Bedrock credentials")
		assert.Contains(t, stderr, "https://bedrock-mantle.us-east-1.api.aws/anthropic")
	})

	t.Run("ambient AWS and Bedrock settings don't select the tier", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		t.Setenv("AWS_REGION", "us-east-1")
		t.Setenv("AWS_PROFILE", "default")
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", fakeBedrockAPIKey)
		t.Setenv("ANTHROPIC_BEDROCK_MANTLE_BASE_URL", "https://endpoint.invalid")
		t.Setenv("ANTHROPIC_API_KEY", "fake-first-party-key")
		req := runAWSLeaf(t)
		assert.Equal(t, "api.anthropic.com", req.URL.Host)
		assert.Equal(t, "Bearer fake-first-party-key", req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("x-api-key"))
	})
}

// TestBedrockSelection pins what selects the Bedrock tier: a region or a
// piped key the user set for this CLI, not AWS or Bedrock settings already on
// the machine.
func TestBedrockSelection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   map[string]string
		argv  []string
		stdin string
		want  bool
	}{
		{name: "--bedrock-region", argv: []string{"--bedrock-region", "us-east-1"}, want: true},
		{name: "ANTHROPIC_BEDROCK_REGION", env: map[string]string{"ANTHROPIC_BEDROCK_REGION": "us-east-1"}, want: true},
		{name: "--bedrock-api-key-stdin", argv: []string{"--bedrock-api-key-stdin"}, stdin: fakeBedrockAPIKey + "\n", want: true},
		{name: "AWS_BEARER_TOKEN_BEDROCK", env: map[string]string{"AWS_BEARER_TOKEN_BEDROCK": fakeBedrockAPIKey}},
		{name: "AWS_REGION", env: map[string]string{"AWS_REGION": "us-east-1"}},
		{name: "AWS_PROFILE", env: map[string]string{"AWS_PROFILE": "default"}},
		{name: "--bedrock-base-url", argv: []string{"--bedrock-base-url", "https://endpoint.invalid"}},
		{name: "ANTHROPIC_BEDROCK_MANTLE_BASE_URL", env: map[string]string{"ANTHROPIC_BEDROCK_MANTLE_BASE_URL": "https://endpoint.invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupAWSTest(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if tc.stdin != "" {
				withStdinCredential(t, tc.stdin)
			}
			var got bool
			root := awsTestRoot(func(ctx context.Context, c *cli.Command) error {
				require.NoError(t, applyStdinCredential(c.Root()))
				got = amazonBedrockFromRoot(c.Root()).AnySet()
				return nil
			})
			require.NoError(t, root.Run(context.Background(), append(append([]string{"ant"}, tc.argv...), "leaf")))
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestBedrockPrecedence pins where the Bedrock tier sits: below an API key,
// an auth token, a named profile, federation, Google Cloud and Claude
// Platform on AWS; above the implicit profile.
func TestBedrockPrecedence(t *testing.T) {
	bedrockArgs := []string{"--bedrock-region", "us-east-1"}

	t.Run("an API key beats Bedrock, with a notice", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		t.Setenv("ANTHROPIC_API_KEY", "fake-first-party-key")
		var req *http.Request
		stderr := captureStderr(t, func() { req = runAWSLeaf(t, bedrockArgs...) })
		assert.Equal(t, "Bearer fake-first-party-key", req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("x-api-key"))
		assert.Contains(t, stderr, "Bedrock (--bedrock-* / ANTHROPIC_BEDROCK_REGION)")
		assert.Contains(t, stderr, "using ANTHROPIC_API_KEY per precedence")
	})

	t.Run("a Bedrock API key alone is not a second source", func(t *testing.T) {
		setupAWSTest(t)
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", fakeBedrockAPIKey)
		stderr := captureStderr(t, func() { runAWSLeaf(t, bedrockArgs...) })
		assert.Empty(t, stderr)
	})

	t.Run("Google Cloud beats Bedrock", func(t *testing.T) {
		setupAWSTest(t)
		useFakeGoogleCredentials(t)
		useFakeAWSCredentials(t, "")
		req := runAWSLeaf(t, append(bedrockArgs, "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_gc")...)
		assertGoogleCloudRequest(t, req, "my-project", "global", "wrkspc_gc")
	})

	t.Run("Claude Platform on AWS beats Bedrock", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		req := runAWSLeaf(t, append(bedrockArgs, "--aws-region", "us-east-1", "--aws-workspace-id", "wrkspc_01")...)
		assertSigV4Request(t, req, "us-east-1", "wrkspc_01")
	})

	t.Run("an AWS API key goes to Claude Platform on AWS, not Bedrock", func(t *testing.T) {
		setupAWSTest(t)
		t.Setenv("AWS_REGION", "us-east-1")
		t.Setenv("ANTHROPIC_AWS_API_KEY", fakeAWSAPIKey)
		t.Setenv("ANTHROPIC_AWS_WORKSPACE_ID", "wrkspc_01")
		req := runAWSLeaf(t, bedrockArgs...)
		assert.Equal(t, "aws-external-anthropic.us-east-1.api.aws", req.URL.Host)
		assert.Equal(t, "Bearer "+fakeAWSAPIKey, req.Header.Get("Authorization"))
	})

	t.Run("Bedrock beats the implicit profile", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		writeAWSTestProfile(t)
		assertBedrockSigV4Request(t, runAWSLeaf(t, bedrockArgs...), "us-east-1")
	})

	t.Run("a named profile beats Bedrock", func(t *testing.T) {
		setupAWSTest(t)
		useFakeAWSCredentials(t, "")
		writeAWSTestProfile(t)
		req := runAWSLeaf(t, append([]string{"--profile", "p"}, bedrockArgs...)...)
		assert.Equal(t, "profile.invalid", req.URL.Host)
		assert.Equal(t, "Bearer profile-token", req.Header.Get("Authorization"))
	})
}

// TestBedrockCredentialsError guards the message a user sees when the
// Bedrock client can't be built: it must say where credentials come from and
// which flags address the endpoint.
func TestBedrockCredentialsError(t *testing.T) {
	setupAWSTest(t)
	_, err := amazonBedrock{APIKey: fakeBedrockAPIKey}.requestOptions(context.Background())
	require.Error(t, err)
	for _, want := range []string{"credential chain", "AWS_BEARER_TOKEN_BEDROCK", "--bedrock-region"} {
		assert.Contains(t, err.Error(), want)
	}

	_, err = amazonBedrock{Region: "us-east-1", BaseURL: "endpoint.invalid"}.requestOptions(context.Background())
	assert.ErrorContains(t, err, "--bedrock-base-url / ANTHROPIC_BEDROCK_MANTLE_BASE_URL")
}

// TestAuthStatusBedrock pins that `auth status` reports the tier the client
// would use, where requests go, and never a key.
func TestAuthStatusBedrock(t *testing.T) {
	setupAWSTest(t)

	out, err := runStatus(t, "--bedrock-region", "us-east-1")
	require.NoError(t, err)
	assert.Regexp(t, `\(active\) \* Bedrock\s+AWS credential chain`, out)
	assert.Regexp(t, `region:\s+us-east-1`, out)
	assert.Regexp(t, `\(active\) \* Bedrock endpoint\s+https://bedrock-mantle\.us-east-1\.api\.aws/anthropic`, out)

	t.Run("the region comes from AWS_REGION", func(t *testing.T) {
		t.Setenv("AWS_REGION", "eu-west-1")
		withStdinCredential(t, fakeBedrockAPIKey+"\n")
		out, err := runStatus(t, "--bedrock-api-key-stdin")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* Bedrock \(API key, --bedrock-api-key-stdin\)`, out)
		assert.Regexp(t, `region:\s+\(from AWS_REGION or AWS_DEFAULT_REGION\)`, out)
		assert.Regexp(t, `\(active\) \* Bedrock endpoint\s+https://bedrock-mantle\.eu-west-1\.api\.aws/anthropic`, out)
	})

	t.Run("a generic base URL is listed but not active", func(t *testing.T) {
		out, err := runStatus(t, "--base-url", "https://anthropic-proxy.invalid", "--bedrock-region", "us-east-1")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* Bedrock endpoint\s+https://bedrock-mantle\.us-east-1\.api\.aws/anthropic`, out)
		assert.Regexp(t, `\n\s+\* --base-url flag\s+https://anthropic-proxy.invalid`, out)
	})

	t.Run("the Bedrock base URL is the active one when set", func(t *testing.T) {
		out, err := runStatus(t, "--bedrock-region", "us-east-1", "--bedrock-base-url", "https://endpoint.invalid")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* --bedrock-base-url / ANTHROPIC_BEDROCK_MANTLE_BASE_URL\s+https://endpoint.invalid`, out)
	})

	t.Run("a Bedrock API key is redacted", func(t *testing.T) {
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "fake-bedrock-api-key-0123456789abcdef")
		out, err := runStatus(t, "--bedrock-region", "us-east-1")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* Bedrock \(API key, AWS_BEARER_TOKEN_BEDROCK\)`, out)
		assert.NotContains(t, out, "0123456789abcdef")
	})

	t.Run("Claude Platform on AWS wins over Bedrock", func(t *testing.T) {
		out, err := runStatus(t, "--aws-region", "us-east-1", "--aws-workspace-id", "wrkspc_01", "--bedrock-region", "us-east-1")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* AWS\s+AWS credential chain`, out)
		assert.Regexp(t, `\n\s+\* Bedrock\s+AWS credential chain`, out)
		assert.Regexp(t, `\(active\) \* AWS gateway\s+https://aws-external-anthropic\.us-east-1\.api\.aws`, out)
	})

	t.Run("an API key wins over Bedrock", func(t *testing.T) {
		out, err := captureStdout(t, func() error {
			t.Setenv("ANTHROPIC_API_KEY", "fake-first-party-key")
			return awsTestRoot(nil).Run(context.Background(), []string{"ant", "--bedrock-region", "us-east-1", "auth", "status"})
		})
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* ANTHROPIC_API_KEY`, out)
		assert.Regexp(t, `\n\s+\* Bedrock\s`, out)
	})
}

// TestBedrockInUse pins what `ant apply` records as the host, how it names
// the credentials, and whether it treats the active profile as the one in use.
func TestBedrockInUse(t *testing.T) {
	var credentials string
	probe := func(t *testing.T, argv ...string) (inUse bool, baseURL string, profile *config.Config) {
		t.Helper()
		root := awsTestRoot(func(ctx context.Context, c *cli.Command) error {
			inUse, baseURL, profile = amazonBedrockInUse(c), currentBaseURL(c), profileInUse(c)
			credentials = describeOrigin(c, core.Origin{BaseURL: baseURL}).Credentials
			return nil
		})
		require.NoError(t, root.Run(context.Background(), append(append([]string{"ant"}, argv...), "leaf")))
		return inUse, baseURL, profile
	}

	t.Run("regional endpoint", func(t *testing.T) {
		setupAWSTest(t)
		inUse, baseURL, _ := probe(t, "--bedrock-region", "us-east-1")
		assert.True(t, inUse)
		assert.Equal(t, "https://bedrock-mantle.us-east-1.api.aws/anthropic", baseURL)
		assert.Equal(t, "Bedrock (AWS credential chain)", credentials)
	})

	t.Run("--bedrock-base-url, API key", func(t *testing.T) {
		setupAWSTest(t)
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", fakeBedrockAPIKey)
		t.Setenv("ANTHROPIC_BASE_URL", "https://anthropic-proxy.invalid")
		_, baseURL, _ := probe(t, "--bedrock-region", "us-east-1", "--bedrock-base-url", "https://endpoint.invalid")
		assert.Equal(t, "https://endpoint.invalid", baseURL)
		assert.Equal(t, "Bedrock (API key)", credentials)
	})

	t.Run("not in use when Claude Platform on AWS wins", func(t *testing.T) {
		setupAWSTest(t)
		inUse, baseURL, _ := probe(t, "--aws-region", "us-east-1", "--bedrock-region", "us-east-1")
		assert.False(t, inUse)
		assert.Equal(t, "https://aws-external-anthropic.us-east-1.api.aws", baseURL)
		assert.Equal(t, "AWS (AWS credential chain)", credentials)
	})

	t.Run("not in use when an API key wins", func(t *testing.T) {
		setupAWSTest(t)
		t.Setenv("ANTHROPIC_API_KEY", "fake-first-party-key")
		inUse, baseURL, _ := probe(t, "--bedrock-region", "us-east-1")
		assert.False(t, inUse)
		assert.Equal(t, defaultBaseURL, baseURL)
	})

	t.Run("the implicit profile is not in use", func(t *testing.T) {
		setupAWSTest(t)
		writeAWSTestProfile(t)
		inUse, _, profile := probe(t, "--bedrock-region", "us-east-1")
		assert.True(t, inUse)
		assert.Nil(t, profile)
	})
}
