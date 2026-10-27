package cmd

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-cli/v2/internal/declarative/core"
	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/config"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const fakeGoogleToken = "fake-google-access-token"

// useFakeGoogleCredentials points Application Default Credentials at a
// throwaway service account key whose token endpoint is a local server, so the
// SDK's real credential discovery and token exchange run without reaching
// Google. The key's project is "adc-project".
func useFakeGoogleCredentials(t *testing.T) {
	t.Helper()
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"`+fakeGoogleToken+`","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(tokens.Close)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	keyFile, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "adc-project",
		"private_key_id": "test",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "test@adc-project.iam.gserviceaccount.com",
		"client_id":      "1",
		"token_uri":      tokens.URL,
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "key.json")
	require.NoError(t, os.WriteFile(path, keyFile, 0o600))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
}

func clearGoogleCloudEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_GOOGLE_CLOUD_PROJECT", "ANTHROPIC_GOOGLE_CLOUD_LOCATION",
		"ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID", "ANTHROPIC_GOOGLE_CLOUD_BASE_URL",
		"GOOGLE_CLOUD_PROJECT",
	} {
		clearEnv(t, k)
	}
}

// googleCloudTestRoot is a fresh tree carrying the global flags
// getDefaultRequestOptions reads, with the same env sources as the real root.
// A fresh tree per run lets each subtest apply its own environment.
func googleCloudTestRoot(action cli.ActionFunc) *cli.Command {
	return &cli.Command{
		Name: "ant",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "api-key-stdin"},
			&cli.BoolFlag{Name: "auth-token-stdin"},
			&cli.StringFlag{Name: "webhook-key"},
			&cli.StringFlag{Name: "base-url", Sources: cli.EnvVars("ANTHROPIC_BASE_URL")},
			&cli.StringFlag{Name: "profile", Sources: cli.EnvVars("ANTHROPIC_PROFILE")},
			&cli.StringFlag{Name: "identity-token"},
			&cli.StringFlag{Name: "identity-token-file"},
			&cli.StringFlag{Name: "federation-rule"},
			&cli.StringFlag{Name: "organization-id"},
			&cli.StringFlag{Name: "service-account-id"},
			&cli.StringFlag{Name: "workspace-id", Sources: cli.EnvVars("ANTHROPIC_WORKSPACE_ID")},
			&cli.StringFlag{Name: "google-cloud-project", Sources: cli.EnvVars("ANTHROPIC_GOOGLE_CLOUD_PROJECT")},
			&cli.StringFlag{Name: "google-cloud-location", Sources: cli.EnvVars("ANTHROPIC_GOOGLE_CLOUD_LOCATION")},
			&cli.StringFlag{Name: "google-cloud-workspace-id", Sources: cli.EnvVars("ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID")},
			&cli.StringFlag{Name: "google-cloud-base-url", Sources: cli.EnvVars("ANTHROPIC_GOOGLE_CLOUD_BASE_URL")},
		},
		Commands: []*cli.Command{{Name: "leaf", Action: action}},
	}
}

// runGoogleCloudLeaf returns the request getDefaultRequestOptions produced
// for a GET of /v1/models under the given global flags.
func runGoogleCloudLeaf(t *testing.T, argv ...string) *http.Request {
	t.Helper()
	var captured *http.Request
	root := googleCloudTestRoot(func(ctx context.Context, c *cli.Command) error {
		opts := append(getDefaultRequestOptions(c),
			option.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				captured = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader("{}")),
					Request:    req,
				}, nil
			})}),
			option.WithMaxRetries(0),
		)
		client := anthropic.NewClient(opts...)
		return client.Get(ctx, "/v1/models", nil, nil)
	})
	require.NoError(t, root.Run(context.Background(), append(append([]string{"ant"}, argv...), "leaf")))
	require.NotNil(t, captured, "no request captured")
	return captured
}

// assertGoogleCloudRequest checks the request went to the gateway's workspace
// endpoint with the Google token and no Anthropic credential. The version
// segment of the path belongs to the SDK and is not pinned here.
func assertGoogleCloudRequest(t *testing.T, req *http.Request, project, location, workspaceID string) {
	t.Helper()
	assert.Equal(t, "claude.googleapis.com", req.URL.Host)
	assert.True(t,
		strings.HasSuffix(req.URL.Path, "/projects/"+project+"/locations/"+location+"/workspaces/"+workspaceID+"/invoke/v1/models"),
		"path %q", req.URL.Path)
	assert.Equal(t, "Bearer "+fakeGoogleToken, req.Header.Get("Authorization"))
	assert.Empty(t, req.Header.Get("x-api-key"))
}

// TestGoogleCloudTier pins when the Google Cloud credential tier is selected
// and what it sends: the gateway URL built from project, location and
// workspace, with a Google token from Application Default Credentials.
func TestGoogleCloudTier(t *testing.T) {
	setup := func(t *testing.T) {
		clearCredentialEnv(t)
		clearGoogleCloudEnv(t)
		resetWarnOnce(t)
		useFakeGoogleCredentials(t)
	}

	t.Run("flags select the tier", func(t *testing.T) {
		setup(t)
		req := runGoogleCloudLeaf(t, "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_01")
		assertGoogleCloudRequest(t, req, "my-project", "global", "wrkspc_01")
	})

	t.Run("environment variables select the tier", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_GOOGLE_CLOUD_PROJECT", "env-project")
		t.Setenv("ANTHROPIC_GOOGLE_CLOUD_LOCATION", "us-east5")
		t.Setenv("ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID", "wrkspc_02")
		req := runGoogleCloudLeaf(t)
		assertGoogleCloudRequest(t, req, "env-project", "us-east5", "wrkspc_02")
	})

	t.Run("the project falls back to the credentials' project", func(t *testing.T) {
		setup(t)
		req := runGoogleCloudLeaf(t, "--google-cloud-workspace-id", "wrkspc_03")
		assertGoogleCloudRequest(t, req, "adc-project", "global", "wrkspc_03")
	})

	t.Run("--google-cloud-base-url takes the request and keeps the Google token", func(t *testing.T) {
		setup(t)
		req := runGoogleCloudLeaf(t, "--google-cloud-workspace-id", "wrkspc_04", "--google-cloud-base-url", "https://gateway.invalid/prefix")
		assert.Equal(t, "gateway.invalid", req.URL.Host)
		assert.Equal(t, "/prefix/v1/models", req.URL.Path)
		assert.Equal(t, "Bearer "+fakeGoogleToken, req.Header.Get("Authorization"))
	})

	// A base URL set up for Anthropic credentials (a proxy, a logging gateway)
	// was never trusted with a Google one, so the tier must not follow it.
	for name, configure := range map[string]func(t *testing.T) []string{
		"--base-url": func(t *testing.T) []string {
			return []string{"--base-url", "https://anthropic-proxy.invalid"}
		},
		"ANTHROPIC_BASE_URL": func(t *testing.T) []string {
			t.Setenv("ANTHROPIC_BASE_URL", "https://anthropic-proxy.invalid")
			return nil
		},
	} {
		t.Run(name+" does not receive the Google token", func(t *testing.T) {
			setup(t)
			argv := append(configure(t), "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_04")
			var req *http.Request
			notice := captureStderr(t, func() { req = runGoogleCloudLeaf(t, argv...) })
			assertGoogleCloudRequest(t, req, "my-project", "global", "wrkspc_04")
			assert.Contains(t, notice, "--base-url / ANTHROPIC_BASE_URL is not used with Google Cloud credentials")
			assert.Contains(t, notice, "--google-cloud-base-url / ANTHROPIC_GOOGLE_CLOUD_BASE_URL")
		})
	}

	t.Run("ANTHROPIC_CUSTOM_HEADERS is not sent", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Custom: one\nAuthorization: Bearer from-headers")
		req := runGoogleCloudLeaf(t, "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_01")
		assertGoogleCloudRequest(t, req, "my-project", "global", "wrkspc_01")
		assert.Empty(t, req.Header.Get("X-Custom"))
	})

	t.Run("a base URL without a scheme is refused before any request", func(t *testing.T) {
		setup(t)
		_, err := googleCloud{BaseURL: "gateway.invalid"}.requestOptions(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--google-cloud-base-url / ANTHROPIC_GOOGLE_CLOUD_BASE_URL")
		assert.Contains(t, err.Error(), "missing a scheme")
	})

	t.Run("the base URL alone does not select the tier", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_GOOGLE_CLOUD_BASE_URL", "https://gateway.invalid")
		req := runGoogleCloudLeaf(t)
		assert.Equal(t, "api.anthropic.com", req.URL.Host,
			"one variable must not both turn on the Google credential and choose where it goes")
		assert.Empty(t, req.Header.Get("Authorization"))
	})

	t.Run("GOOGLE_CLOUD_PROJECT alone does not select the tier", func(t *testing.T) {
		setup(t)
		t.Setenv("GOOGLE_CLOUD_PROJECT", "unrelated-project")
		req := runGoogleCloudLeaf(t)
		assert.Equal(t, "api.anthropic.com", req.URL.Host,
			"a variable set on most Google Cloud machines must not redirect the CLI")
		assert.Empty(t, req.Header.Get("Authorization"))
	})

	t.Run("an API key wins and the notice names both sources", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_API_KEY", "test-fake-api-key-not-real")
		var req *http.Request
		notice := captureStderr(t, func() {
			req = runGoogleCloudLeaf(t, "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_05")
		})
		assert.Equal(t, "api.anthropic.com", req.URL.Host)
		assert.Equal(t, []string{"Bearer test-fake-api-key-not-real"}, req.Header.Values("Authorization"),
			"the Google token must not ride along with a winning API key")
		assert.Contains(t, notice, "Google Cloud")
		assert.Contains(t, notice, "using ANTHROPIC_API_KEY per precedence")
		assert.NotContains(t, notice, "test-fake-api-key-not-real")
	})

	t.Run("an auth token wins", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "test-fake-auth-token-not-real")
		req := runGoogleCloudLeaf(t, "--google-cloud-workspace-id", "wrkspc_06")
		assert.Equal(t, "api.anthropic.com", req.URL.Host)
		assert.Equal(t, "Bearer test-fake-auth-token-not-real", req.Header.Get("Authorization"))
	})
}

// TestGoogleCloudVsProfile pins the tier's place next to profiles: it beats a
// profile that merely happens to be active, and loses to one the user named.
func TestGoogleCloudVsProfile(t *testing.T) {
	setup := func(t *testing.T) {
		clearCredentialEnv(t)
		clearGoogleCloudEnv(t)
		resetWarnOnce(t)
		useFakeGoogleCredentials(t)
		dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
		credentials := config.ProfileCredentialsPath(dir, "p")
		require.NoError(t, config.SaveProfile(dir, "p", &config.Config{
			BaseURL: "https://profile.invalid",
			AuthenticationInfo: &config.AuthenticationInfo{
				Type:            config.AuthenticationTypeUserOAuth,
				CredentialsPath: credentials,
				UserOAuth:       &config.UserOAuth{},
			},
		}))
		require.NoError(t, config.WriteCredentials(credentials, config.Credentials{AccessToken: "profile-token"}))
		require.NoError(t, config.SetActiveProfile(dir, "p"))
	}

	t.Run("Google Cloud beats the implicit profile", func(t *testing.T) {
		setup(t)
		req := runGoogleCloudLeaf(t, "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_01")
		assertGoogleCloudRequest(t, req, "my-project", "global", "wrkspc_01")
	})

	t.Run("a named profile beats Google Cloud", func(t *testing.T) {
		setup(t)
		req := runGoogleCloudLeaf(t, "--profile", "p", "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_01")
		assert.Equal(t, "profile.invalid", req.URL.Host)
		assert.Equal(t, "Bearer profile-token", req.Header.Get("Authorization"))
	})
}

// TestGoogleCloudCredentialsError guards the message a user sees when no
// Google credentials can be found: it must say how to sign in and which flags
// address the gateway.
func TestGoogleCloudCredentialsError(t *testing.T) {
	clearGoogleCloudEnv(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))

	_, err := googleCloud{Project: "my-project", WorkspaceID: "wrkspc_01"}.requestOptions(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gcloud auth application-default login")
	assert.Contains(t, err.Error(), "GOOGLE_APPLICATION_CREDENTIALS")
	assert.Contains(t, err.Error(), "--google-cloud-project")
}

// TestGoogleCloudInUse pins what `ant apply` records as the host, how it
// names the credentials, and whether it treats the active profile as the one
// in use, by the same precedence the client applies.
func TestGoogleCloudInUse(t *testing.T) {
	var credentials string
	probe := func(t *testing.T, argv ...string) (inUse bool, baseURL string, profile *config.Config) {
		t.Helper()
		root := googleCloudTestRoot(func(ctx context.Context, c *cli.Command) error {
			inUse, baseURL, profile = googleCloudInUse(c), currentBaseURL(c), profileInUse(c)
			credentials = describeOrigin(c, core.Origin{BaseURL: baseURL}).Credentials
			return nil
		})
		require.NoError(t, root.Run(context.Background(), append(append([]string{"ant"}, argv...), "leaf")))
		return inUse, baseURL, profile
	}
	setup := func(t *testing.T) {
		clearCredentialEnv(t)
		clearGoogleCloudEnv(t)
	}

	t.Run("not configured", func(t *testing.T) {
		setup(t)
		inUse, baseURL, _ := probe(t)
		assert.False(t, inUse)
		assert.Equal(t, defaultBaseURL, baseURL)

		inUse, baseURL, _ = probe(t, "--google-cloud-base-url", "https://gateway.invalid")
		assert.False(t, inUse, "the base URL alone does not select the tier")
		assert.Equal(t, defaultBaseURL, baseURL)
	})

	t.Run("configured", func(t *testing.T) {
		setup(t)
		inUse, baseURL, profile := probe(t, "--google-cloud-workspace-id", "wrkspc_01")
		assert.True(t, inUse)
		assert.Equal(t, googleCloudHost, baseURL)
		assert.Nil(t, profile)
		assert.Equal(t, "Google Cloud (Application Default Credentials)", credentials)
	})

	t.Run("the recorded host follows the Google Cloud base URL, not the generic one", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_BASE_URL", "https://anthropic-proxy.invalid")
		_, baseURL, _ := probe(t, "--google-cloud-workspace-id", "wrkspc_01")
		assert.Equal(t, googleCloudHost, baseURL, "requests do not go to ANTHROPIC_BASE_URL, so the lockfile must not say they did")

		t.Setenv("ANTHROPIC_GOOGLE_CLOUD_BASE_URL", "https://gateway.invalid")
		_, baseURL, _ = probe(t, "--google-cloud-workspace-id", "wrkspc_01")
		assert.Equal(t, "https://gateway.invalid", baseURL)
	})

	t.Run("an API key wins", func(t *testing.T) {
		setup(t)
		t.Setenv("ANTHROPIC_API_KEY", "test-fake-api-key-not-real")
		inUse, baseURL, _ := probe(t, "--google-cloud-workspace-id", "wrkspc_01")
		assert.False(t, inUse)
		assert.Equal(t, defaultBaseURL, baseURL)
		assert.Equal(t, "API key (ANTHROPIC_API_KEY)", credentials)
	})

	t.Run("an API key piped on stdin wins", func(t *testing.T) {
		setup(t)
		argv := []string{"--api-key-stdin", "--google-cloud-workspace-id", "wrkspc_01", "leaf"}
		withStdinCredential(t, "test-fake-api-key-not-real\n")
		var inUse bool
		var baseURL string
		root := googleCloudTestRoot(func(ctx context.Context, c *cli.Command) error {
			if err := applyStdinCredential(c.Root()); err != nil {
				return err
			}
			inUse, baseURL = googleCloudInUse(c), currentBaseURL(c)
			credentials = describeOrigin(c, core.Origin{BaseURL: baseURL}).Credentials
			return nil
		})
		require.NoError(t, root.Run(context.Background(), append([]string{"ant"}, argv...)))
		assert.False(t, inUse)
		assert.Equal(t, defaultBaseURL, baseURL)
		assert.Equal(t, "API key (--api-key-stdin)", credentials)
	})

	t.Run("an implicit profile is not in use", func(t *testing.T) {
		setup(t)
		dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
		credentials := config.ProfileCredentialsPath(dir, "p")
		require.NoError(t, config.SaveProfile(dir, "p", &config.Config{
			BaseURL: "https://profile.invalid",
			AuthenticationInfo: &config.AuthenticationInfo{
				Type:            config.AuthenticationTypeUserOAuth,
				CredentialsPath: credentials,
				UserOAuth:       &config.UserOAuth{},
			},
		}))
		require.NoError(t, config.WriteCredentials(credentials, config.Credentials{AccessToken: "profile-token"}))
		require.NoError(t, config.SetActiveProfile(dir, "p"))

		inUse, baseURL, profile := probe(t, "--google-cloud-workspace-id", "wrkspc_01")
		assert.True(t, inUse)
		assert.Equal(t, googleCloudHost, baseURL)
		assert.Nil(t, profile, "the profile's base URL and Console link must not be attributed to a Google Cloud run")
	})
}

// TestAuthStatusGoogleCloud pins that `auth status` reports the tier the
// client would use, where requests go, and never a token.
func TestAuthStatusGoogleCloud(t *testing.T) {
	clearCredentialEnv(t)
	clearGoogleCloudEnv(t)
	useFakeGoogleCredentials(t)

	out, err := runStatus(t, "--google-cloud-project", "my-project", "--google-cloud-workspace-id", "wrkspc_01")
	require.NoError(t, err)
	assert.Regexp(t, `\(active\) \* Google Cloud\s+Application Default Credentials`, out)
	assert.Regexp(t, `project:\s+my-project`, out)
	assert.Regexp(t, `location:\s+global`, out)
	assert.Regexp(t, `workspace_id:\s+wrkspc_01`, out)
	assert.Regexp(t, `\(active\) \* Google Cloud gateway\s+`+googleCloudHost, out)
	assert.NotContains(t, out, fakeGoogleToken)

	t.Run("a generic base URL is listed but not active", func(t *testing.T) {
		out, err := runStatus(t, "--base-url", "https://anthropic-proxy.invalid", "--google-cloud-workspace-id", "wrkspc_01")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* Google Cloud gateway\s+`+googleCloudHost, out)
		assert.Regexp(t, `\n\s+\* --base-url flag\s+https://anthropic-proxy.invalid`, out)
	})

	t.Run("the Google Cloud base URL is the active one when set", func(t *testing.T) {
		out, err := runStatus(t, "--google-cloud-workspace-id", "wrkspc_01", "--google-cloud-base-url", "https://gateway.invalid")
		require.NoError(t, err)
		assert.Regexp(t, `\(active\) \* --google-cloud-base-url / ANTHROPIC_GOOGLE_CLOUD_BASE_URL\s+https://gateway.invalid`, out)
	})
}
