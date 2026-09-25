package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/crush/internal/tlsconfig"
	"github.com/stretchr/testify/require"
)

// TestDiscoverModelsTLS covers the case model discovery exists for: a local
// server serving https with a certificate no public CA signed. Discovery has to
// succeed there, otherwise a working provider would come up with no models.
func TestDiscoverModelsTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","object":"model"}]}`))
	}))
	defer server.Close()

	// Without TLS settings the self-signed certificate must fail discovery.
	_, err := DiscoverModels(context.Background(), Config{
		ID:      "local",
		BaseURL: server.URL + "/v1",
	}, &mockResolver{})
	require.Error(t, err, "an untrusted certificate must fail discovery")

	models, err := DiscoverModels(context.Background(), Config{
		ID:      "local",
		BaseURL: server.URL + "/v1",
		TLS:     tlsconfig.Options{SkipVerify: true},
	}, &mockResolver{})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "model-a", models[0].ID)
}

// TestDiscoverModelsBadTLSConfig proves a certificate that cannot be read fails
// discovery loudly rather than falling back to an unverified connection.
func TestDiscoverModelsBadTLSConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, err := DiscoverModels(context.Background(), Config{
		ID:      "local",
		BaseURL: server.URL + "/v1",
		TLS:     tlsconfig.Options{CACert: "/nonexistent/ca.pem"},
	}, &mockResolver{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "CA certificate")
}

// TestClientForNoTLSKeepsSharedClient keeps the default path allocation free.
func TestClientForNoTLSKeepsSharedClient(t *testing.T) {
	client, err := clientFor(tlsconfig.Options{})
	require.NoError(t, err)
	require.Same(t, httpClient, client, "no TLS settings must reuse the shared client")

	client, err = clientFor(tlsconfig.Options{SkipVerify: true})
	require.NoError(t, err)
	require.NotSame(t, httpClient, client)
	require.NotNil(t, client.Transport, "the per-provider client must carry its own transport")
}
