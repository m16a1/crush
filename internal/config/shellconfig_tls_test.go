package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellConfigProviderTLSFlags(t *testing.T) {
	store := loadCrushSh(t, `provider add myllm \
  --type openai-compat \
  --base-url "https://localhost:8443/v1" \
  --api-key "sk-test" \
  --skip-tls-verify true \
  --tls-ca-cert "$HOME/certs/ca.pem" \
  --tls-client-cert "$HOME/certs/client.pem" \
  --tls-client-key "$HOME/certs/client-key.pem"
model add myllm/foo --context-window 8000`)

	p, ok := store.Config().Providers.Get("myllm")
	require.True(t, ok, "myllm provider should be configured")
	require.True(t, p.SkipTLSVerify)
	// Certificate paths go through the same shell expansion as api_key and
	// base_url, so the stored value is already absolute.
	home := os.Getenv("HOME")
	require.Equal(t, filepath.Join(home, "certs", "ca.pem"), p.TLSCACert)
	require.Equal(t, filepath.Join(home, "certs", "client.pem"), p.TLSClientCert)
	require.Equal(t, filepath.Join(home, "certs", "client-key.pem"), p.TLSClientKey)

	resolved, err := p.ResolvedTLS(store.Resolver())
	require.NoError(t, err)
	require.True(t, resolved.SkipVerify)
	require.Equal(t, p.TLSCACert, resolved.CACert)
	require.NoError(t, resolved.Validate())
}

func TestShellConfigMCPTLSFlags(t *testing.T) {
	store := loadCrushSh(t, `mcp add local --type http --url "https://localhost:9443/mcp" \
  --tls-ca-cert "/etc/ssl/private-ca.pem"`)

	mcp, ok := store.Config().MCP["local"]
	require.True(t, ok, "local MCP should be configured")
	require.False(t, mcp.SkipTLSVerify)
	require.Equal(t, "/etc/ssl/private-ca.pem", mcp.TLSCACert)

	resolved, err := mcp.ResolvedTLS(store.Resolver())
	require.NoError(t, err)
	require.Equal(t, "/etc/ssl/private-ca.pem", resolved.CACert)
	require.Empty(t, resolved.ClientCert)
}

// TestShellConfigTLSPathsResolveThroughShell proves a certificate path defined
// indirectly, as a variable holding a variable, still reaches the transport,
// since certificate paths go through the same expansion as api_key.
func TestShellConfigTLSPathsResolveThroughShell(t *testing.T) {
	store := loadCrushSh(t, `MY_CERTS=$HOME/certs
provider add myllm --type openai-compat --base-url "https://localhost:8443/v1" --tls-ca-cert "$MY_CERTS/ca.pem"
model add myllm/foo --context-window 8000`)

	p, ok := store.Config().Providers.Get("myllm")
	require.True(t, ok)

	resolved, err := p.ResolvedTLS(store.Resolver())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(os.Getenv("HOME"), "certs", "ca.pem"), resolved.CACert)
}

func TestShellConfigProviderTLSPairingIsChecked(t *testing.T) {
	// A client certificate without its key is accepted into the config but
	// rejected when the transport is built, so the mistake surfaces as a
	// provider error rather than a confusing handshake failure. The pairing
	// check itself is covered in the tlsconfig package; this only asserts the
	// config round-trips what the user wrote.
	store := loadCrushSh(t, `provider add myllm --type openai-compat --base-url "https://localhost:8443/v1" \
  --tls-client-cert "/cert.pem"
model add myllm/foo --context-window 8000`)

	p, ok := store.Config().Providers.Get("myllm")
	require.True(t, ok)
	require.Equal(t, "/cert.pem", p.TLSClientCert)
	require.Empty(t, p.TLSClientKey)

	require.Error(t, p.TLS().Validate())
}

func TestShellConfigProviderTLSDefaultsOff(t *testing.T) {
	store := loadCrushSh(t, `provider add myllm --type openai-compat --base-url "https://api.example.com/v1"
model add myllm/foo --context-window 8000`)

	p, ok := store.Config().Providers.Get("myllm")
	require.True(t, ok)
	require.Equal(t, "https://api.example.com/v1", p.BaseURL)
	require.False(t, p.SkipTLSVerify)
	require.Empty(t, p.TLSCACert)
	require.Empty(t, p.TLSClientCert)
	require.Empty(t, p.TLSClientKey)
	require.True(t, p.TLS().Empty(), "a provider with no TLS config must leave the default transport alone")
}
