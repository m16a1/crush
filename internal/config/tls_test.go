package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/tlsconfig"
	"github.com/stretchr/testify/require"
)

// tlsTestResolver mimics the shell resolver for the single pass the config
// layer makes: $NAME becomes its value, anything else is returned unchanged.
type tlsTestResolver map[string]string

func (r tlsTestResolver) ResolveValue(v string) (string, error) {
	rest, ok := strings.CutPrefix(v, "$")
	if !ok {
		return v, nil
	}
	name, tail, found := strings.Cut(rest, "/")
	if !found {
		return r[name], nil
	}
	return r[name] + "/" + tail, nil
}

// failingResolver reports an error for every value, standing in for a shell
// expansion that cannot run.
type failingResolver struct{}

func (failingResolver) ResolveValue(string) (string, error) {
	return "", errors.New("expansion failed")
}

func TestProviderTLSMapping(t *testing.T) {
	t.Parallel()

	cfg := ProviderConfig{
		SkipTLSVerify: true,
		TLSCACert:     "/etc/ssl/ca.pem",
		TLSClientCert: "/etc/ssl/client.pem",
		TLSClientKey:  "/etc/ssl/client-key.pem",
	}

	got := cfg.TLS()
	require.Equal(t, tlsconfig.Options{
		SkipVerify: true,
		CACert:     "/etc/ssl/ca.pem",
		ClientCert: "/etc/ssl/client.pem",
		ClientKey:  "/etc/ssl/client-key.pem",
	}, got)
}

func TestProviderResolvedTLS(t *testing.T) {
	t.Parallel()

	cfg := ProviderConfig{
		TLSCACert:     "$CA_DIR/ca.pem",
		TLSClientCert: "$CERT_DIR/client.pem",
		TLSClientKey:  "$CERT_DIR/client-key.pem",
	}
	resolver := tlsTestResolver{"CA_DIR": "/etc/ssl", "CERT_DIR": "/home/me/certs"}

	got, err := cfg.ResolvedTLS(resolver)
	require.NoError(t, err)
	require.Equal(t, "/etc/ssl/ca.pem", got.CACert)
	require.Equal(t, "/home/me/certs/client.pem", got.ClientCert)
	require.Equal(t, "/home/me/certs/client-key.pem", got.ClientKey)
	require.False(t, got.SkipVerify)
}

func TestProviderResolvedTLSLeavesUnsetPathsAlone(t *testing.T) {
	t.Parallel()

	// An unset path must stay empty rather than being handed to the resolver,
	// since empty is what tells the transport to leave that setting off.
	cfg := ProviderConfig{SkipTLSVerify: true}

	got, err := cfg.ResolvedTLS(failingResolver{})
	require.NoError(t, err)
	require.Equal(t, tlsconfig.Options{SkipVerify: true}, got)
}

func TestProviderResolvedTLSErrorNamesTheField(t *testing.T) {
	t.Parallel()

	cfg := ProviderConfig{TLSCACert: "$CA"}

	_, err := cfg.ResolvedTLS(failingResolver{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tls_ca_cert")
}

func TestMCPTLSMapping(t *testing.T) {
	t.Parallel()

	cfg := MCPConfig{
		SkipTLSVerify: true,
		TLSCACert:     "$CA",
		TLSClientCert: "client.pem",
		TLSClientKey:  "client-key.pem",
	}
	resolver := tlsTestResolver{"CA": "/etc/ssl/ca.pem"}

	require.Equal(t, tlsconfig.Options{
		SkipVerify: true,
		CACert:     "$CA",
		ClientCert: "client.pem",
		ClientKey:  "client-key.pem",
	}, cfg.TLS())

	got, err := cfg.ResolvedTLS(resolver)
	require.NoError(t, err)
	require.Equal(t, "/etc/ssl/ca.pem", got.CACert)
}

func TestMCPResolvedTLSErrorNamesTheField(t *testing.T) {
	t.Parallel()

	cfg := MCPConfig{TLSClientKey: "$KEY"}

	_, err := cfg.ResolvedTLS(failingResolver{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tls_client_key")
}

func TestTLSConfigFieldsUnmarshal(t *testing.T) {
	t.Parallel()

	// These names are the documented config surface, so a tag typo would be
	// invisible to the compiler but break every user's config.
	const providerJSON = `{
		"skip_tls_verify": true,
		"tls_ca_cert": "/etc/ssl/ca.pem",
		"tls_client_cert": "client.pem",
		"tls_client_key": "client-key.pem"
	}`

	var provider ProviderConfig
	require.NoError(t, json.Unmarshal([]byte(providerJSON), &provider))
	require.True(t, provider.SkipTLSVerify)
	require.Equal(t, "/etc/ssl/ca.pem", provider.TLSCACert)
	require.Equal(t, "client.pem", provider.TLSClientCert)
	require.Equal(t, "client-key.pem", provider.TLSClientKey)

	var mcp MCPConfig
	require.NoError(t, json.Unmarshal([]byte(providerJSON), &mcp))
	require.True(t, mcp.SkipTLSVerify)
	require.Equal(t, "/etc/ssl/ca.pem", mcp.TLSCACert)
	require.Equal(t, "client.pem", mcp.TLSClientCert)
	require.Equal(t, "client-key.pem", mcp.TLSClientKey)
}
