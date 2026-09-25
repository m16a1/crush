package tlsconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmpty(t *testing.T) {
	t.Parallel()

	require.True(t, Options{}.Empty())
	require.False(t, Options{SkipVerify: true}.Empty())
	require.False(t, Options{CACert: "ca.pem"}.Empty())
	require.False(t, Options{ClientCert: "c.pem", ClientKey: "k.pem"}.Empty())
}

func TestValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, Options{}.Validate())
	require.NoError(t, Options{ClientCert: "c.pem", ClientKey: "k.pem"}.Validate())

	err := Options{ClientCert: "c.pem"}.Validate()
	require.Error(t, err, "a client certificate without its key must be rejected")

	err = Options{ClientKey: "k.pem"}.Validate()
	require.Error(t, err, "a client key without its certificate must be rejected")
}

func TestTransportNoOptionsKeepsDefault(t *testing.T) {
	t.Parallel()

	tr, err := Options{}.Transport(nil)
	require.NoError(t, err)
	require.Nil(t, tr, "no settings must leave the caller on the default transport")
}

func TestTransportSkipVerify(t *testing.T) {
	t.Parallel()

	tr, err := Options{SkipVerify: true}.Transport(nil)
	require.NoError(t, err)
	require.NotNil(t, tr.TLSClientConfig)
	require.True(t, tr.TLSClientConfig.InsecureSkipVerify)
}

func TestTransportDoesNotLeakIntoSharedTransport(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport)
	baseBefore := base.TLSClientConfig

	tr, err := Options{SkipVerify: true}.Transport(base)
	require.NoError(t, err)
	require.NotSame(t, base, tr, "the transport must be a clone, not the shared one")

	// Cloning the global default transport makes Go initialize its HTTP/2
	// settings in place, so the shared config can change shape. The provider's
	// own TLS settings must never appear there.
	if base.TLSClientConfig != nil {
		require.False(t, base.TLSClientConfig.InsecureSkipVerify,
			"the shared transport must not become unverified")
		require.Nil(t, base.TLSClientConfig.RootCAs,
			"the shared transport's roots must not be replaced")
	}
	require.NotSame(t, baseBefore, base.TLSClientConfig)
	require.True(t, tr.TLSClientConfig.InsecureSkipVerify, "the returned transport carries the setting")
}

func TestTransportCACert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ca := generateCert(t, caTemplate(), nil, nil)
	leaf := generateCert(t, leafTemplate(), ca, ca)

	caPath := writePEM(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", ca.der)

	tr, err := Options{CACert: caPath}.Transport(nil)
	require.NoError(t, err)
	require.NotNil(t, tr.TLSClientConfig.RootCAs)

	_, err = leaf.cert.Verify(x509.VerifyOptions{
		Roots:     tr.TLSClientConfig.RootCAs,
		DNSName:   "localhost",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err, "the configured CA must trust its leaf")
}

func TestTransportCACertErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := Options{CACert: filepath.Join(dir, "missing.pem")}.Transport(nil)
	require.Error(t, err)

	emptyPath := writePEM(t, filepath.Join(dir, "empty.pem"), "CERTIFICATE", nil)
	_, err = Options{CACert: emptyPath}.Transport(nil)
	require.Error(t, err, "a PEM file with no certificates must be rejected")
}

func TestTransportClientCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ca := generateCert(t, caTemplate(), nil, nil)
	client := generateCert(t, leafTemplate(), ca, ca)

	certPath := writePEM(t, filepath.Join(dir, "client.pem"), "CERTIFICATE", client.der)
	keyPath := writePEM(t, filepath.Join(dir, "client-key.pem"), "EC PRIVATE KEY", client.keyDER)

	tr, err := Options{ClientCert: certPath, ClientKey: keyPath}.Transport(nil)
	require.NoError(t, err)
	require.Len(t, tr.TLSClientConfig.Certificates, 1, "the client certificate must be presented")
	require.Nil(t, tr.TLSClientConfig.RootCAs, "a client certificate alone must not replace the roots")
}

func TestTransportClientCertAndCA(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ca := generateCert(t, caTemplate(), nil, nil)
	client := generateCert(t, leafTemplate(), ca, ca)

	caPath := writePEM(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", ca.der)
	certPath := writePEM(t, filepath.Join(dir, "client.pem"), "CERTIFICATE", client.der)
	keyPath := writePEM(t, filepath.Join(dir, "client-key.pem"), "EC PRIVATE KEY", client.keyDER)

	tr, err := Options{
		SkipVerify: true,
		CACert:     caPath,
		ClientCert: certPath,
		ClientKey:  keyPath,
	}.Transport(nil)
	require.NoError(t, err)
	require.True(t, tr.TLSClientConfig.InsecureSkipVerify)
	require.NotNil(t, tr.TLSClientConfig.RootCAs)
	require.Len(t, tr.TLSClientConfig.Certificates, 1)
}

func TestTransportClientCertErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ca := generateCert(t, caTemplate(), nil, nil)
	leaf := generateCert(t, leafTemplate(), ca, ca)

	certPath := writePEM(t, filepath.Join(dir, "client.pem"), "CERTIFICATE", leaf.der)

	_, err := Options{ClientCert: certPath}.Transport(nil)
	require.Error(t, err, "the pair is required")

	_, err = Options{ClientCert: certPath, ClientKey: filepath.Join(dir, "missing.pem")}.Transport(nil)
	require.Error(t, err)

	// A key that does not match the certificate must be rejected.
	other := generateCert(t, leafTemplate(), ca, ca)
	otherPath := writePEM(t, filepath.Join(dir, "other-key.pem"), "EC PRIVATE KEY", other.keyDER)
	_, err = Options{ClientCert: certPath, ClientKey: otherPath}.Transport(nil)
	require.Error(t, err)
}

// certPair is a generated certificate with its key and DER encodings.
type certPair struct {
	cert   *x509.Certificate
	der    []byte
	key    *ecdsa.PrivateKey
	keyDER []byte
}

func caTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "crush test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
}

func leafTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "crush test leaf"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
}

// generateCert signs template with parent, or self-signs when parent is nil.
func generateCert(t *testing.T, template *x509.Certificate, parent, parentKey *certPair) *certPair {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	signer, signerKey := template, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	return &certPair{cert: cert, der: der, key: key, keyDER: keyDER}
}

// writePEM writes a single PEM block to path and returns the path.
func writePEM(t *testing.T, path, blockType string, der []byte) string {
	t.Helper()

	var block pem.Block
	block.Type = blockType
	block.Bytes = der
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&block), 0o600))
	return path
}
