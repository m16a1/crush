// Package tlsconfig turns a connection's TLS settings into an HTTP transport.
//
// It exists for the two cases the default transport cannot serve: a server
// whose certificate is signed by a private or self-signed CA, and a server that
// requires a client certificate (mutual TLS). Verification can also be turned
// off outright for a local server whose certificate cannot be trusted any other
// way.
//
// The settings are per connection, so enabling them never changes how Crush
// talks to any other host, and an empty [Options] leaves the caller's transport
// exactly as it was.
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// Options are the TLS settings of a single provider or MCP server, with any
// shell expansion already applied to the paths.
type Options struct {
	// SkipVerify disables verification of the server's certificate chain and
	// hostname. It removes the only protection against an interceptor.
	SkipVerify bool
	// CACert is the path to a PEM file holding certificates to trust in
	// addition to the system roots. Use it for a server whose certificate is
	// signed by a private CA or is self-signed.
	CACert string
	// ClientCert and ClientKey are the path to a PEM client certificate and
	// its private key, presented to the server for mutual TLS. Both must be
	// set together.
	ClientCert string
	ClientKey  string
}

// Empty reports whether no TLS setting is configured, in which case callers can
// keep the default transport.
func (o Options) Empty() bool {
	return !o.SkipVerify && o.CACert == "" && o.ClientCert == "" && o.ClientKey == ""
}

// Validate reports whether the options are usable as a set, so a mistake in the
// config surfaces before the first request rather than as a connection error.
func (o Options) Validate() error {
	if o.Empty() {
		return nil
	}
	if (o.ClientCert == "") != (o.ClientKey == "") {
		return errors.New("tls_client_cert and tls_client_key must be set together")
	}
	return nil
}

// Transport returns base with these settings applied, cloning it so the shared
// transport is never mutated. A nil base starts from a clone of
// [http.DefaultTransport]. When no setting is configured base is returned
// unchanged, so the default path keeps using [http.DefaultTransport] itself.
func (o Options) Transport(base *http.Transport) (*http.Transport, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if o.Empty() {
		return base, nil
	}

	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	tr := base.Clone()

	cfg := &tls.Config{}
	if o.SkipVerify {
		cfg.InsecureSkipVerify = true
	}
	if o.CACert != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(o.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in CA certificate %s", o.CACert)
		}
		cfg.RootCAs = pool
	}
	if o.ClientCert != "" {
		cert, err := tls.LoadX509KeyPair(o.ClientCert, o.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	tr.TLSClientConfig = cfg
	return tr, nil
}
