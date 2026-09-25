package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/tlsconfig"
	"github.com/stretchr/testify/require"
)

// TestProviderHTTPClientTLS proves the client really carries the setting rather
// than only storing it: a self-signed server is unreachable by default and
// reachable once verification is skipped.
func TestProviderHTTPClientTLS(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "myllm", config.ProviderConfig{
		ID:   "myllm",
		Type: catwalk.TypeOpenAICompat,
	})

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	def, err := coord.providerHTTPClient(tlsconfig.Options{})
	require.NoError(t, err)
	resp, err := def.Get(server.URL)
	require.Error(t, err, "a self-signed certificate must be rejected by default")
	require.Nil(t, resp)

	insecure, err := coord.providerHTTPClient(tlsconfig.Options{SkipVerify: true})
	require.NoError(t, err)
	resp, err = insecure.Get(server.URL)
	require.NoError(t, err, "skipping verification must make the server reachable")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestProviderHTTPClientBadCertErrors proves a certificate that cannot be read
// fails the client instead of silently falling back to an unverified one.
func TestProviderHTTPClientBadCertErrors(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "myllm", config.ProviderConfig{
		ID:   "myllm",
		Type: catwalk.TypeOpenAICompat,
	})

	_, err := coord.providerHTTPClient(tlsconfig.Options{CACert: "/nonexistent/ca.pem"})
	require.Error(t, err)

	_, err = coord.providerHTTPClient(tlsconfig.Options{ClientCert: "client.pem"})
	require.Error(t, err, "a client certificate without its key must be rejected")
}

// TestBuildProviderPropagatesTLSErrors covers every provider builder: a
// certificate the transport cannot build has to fail the provider rather than
// be dropped on the floor by one of the branches.
func TestBuildProviderPropagatesTLSErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		providerID string
		provider   catwalk.Type
	}{
		{"openai", "openai", catwalk.Type(openai.Name)},
		{"anthropic", "anthropic", catwalk.Type(anthropic.Name)},
		{"openrouter", "openrouter", catwalk.Type(openrouter.Name)},
		{"vercel", "vercel", catwalk.Type(vercel.Name)},
		{"azure", "azure", catwalk.Type(azure.Name)},
		{"bedrock", "bedrock", catwalk.Type(bedrock.Name)},
		{"google", "google", catwalk.Type(google.Name)},
		{"google-vertex", "google-vertex", catwalk.Type("google-vertex")},
		{"openai-compat", "myllm", catwalk.Type(openaicompat.Name)},
		{"copilot", "copilot", catwalk.Type(openaicompat.Name)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := testEnv(t)
			providerCfg := config.ProviderConfig{
				ID:        tt.providerID,
				Type:      tt.provider,
				BaseURL:   "https://localhost:8443/v1",
				APIKey:    "sk-test",
				TLSCACert: "/nonexistent/ca.pem",
			}
			coord := newTestCoordinator(t, env, tt.providerID, providerCfg)

			_, err := coord.buildProvider(providerCfg, config.SelectedModel{
				Model:    "test-model",
				Provider: tt.providerID,
			}, false)
			require.Error(t, err, "the unreadable CA must fail every provider type")
			require.Contains(t, err.Error(), "CA certificate")
		})
	}
}
