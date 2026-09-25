package hyper

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agenthyper "github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/stretchr/testify/require"
)

var (
	server   *httptest.Server
	current  atomic.Pointer[http.HandlerFunc]
	serialMu sync.Mutex
)

func TestMain(m *testing.M) {
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := current.Load()
		if h == nil {
			http.Error(w, "no handler installed", http.StatusInternalServerError)
			return
		}
		(*h)(w, r)
	}))
	// BaseURL is memoised on first use, so the environment has to be set before
	// any test calls into the package.
	os.Setenv("HYPER_URL", server.URL)

	code := m.Run()

	server.Close()
	os.Exit(code)
}

// serveWith installs a handler for the duration of one test. Tests using it
// must not call t.Parallel: they share the single memoised base URL.
func serveWith(t *testing.T, h http.HandlerFunc) {
	t.Helper()

	serialMu.Lock()
	hh := h
	current.Store(&hh)
	t.Cleanup(func() {
		current.Store(nil)
		serialMu.Unlock()
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := io.WriteString(w, body)
	require.NoError(t, err)
}

func TestMainPointsThePackageAtTheTestServer(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, `{"ok":true}`)
	})

	require.True(t, strings.HasPrefix(agenthyper.BaseURL(), server.URL), "sanity: HYPER_URL must win over the default")
}

func TestInitiateDeviceAuthSendsTheExpectedRequestAndParsesTheResponse(t *testing.T) {
	var body string
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/device/auth", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.Equal(t, "crush", r.Header.Get("User-Agent"))
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body = string(raw)

		writeJSON(t, w, http.StatusOK, `{
			"device_code":"dev-123",
			"user_code":"ABCD-EFGH",
			"verification_url":"https://example.test/verify",
			"expires_in":900
		}`)
	})

	resp, err := InitiateDeviceAuth(t.Context())
	require.NoError(t, err)
	require.Equal(t, "dev-123", resp.DeviceCode)
	require.Equal(t, "ABCD-EFGH", resp.UserCode)
	require.Equal(t, "https://example.test/verify", resp.VerificationURL)
	require.Equal(t, 900, resp.ExpiresIn)
	require.Contains(t, body, `"device_name"`)
}

func TestInitiateDeviceAuthReportsAnHTTPFailure(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusInternalServerError, `{"error":"boom"}`)
	})

	_, err := InitiateDeviceAuth(t.Context())
	require.ErrorContains(t, err, "device auth failed")
	require.ErrorContains(t, err, "status 500")
}

func TestInitiateDeviceAuthRejectsAMalformedBody(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, "not json")
	})

	_, err := InitiateDeviceAuth(t.Context())
	require.ErrorContains(t, err, "unmarshal response")
}

func TestPollOnceReportsAuthorizationPending(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/device/auth/dev-123", r.URL.Path)
		writeJSON(t, w, http.StatusOK, `{"error":"authorization_pending"}`)
	})

	result, err := pollOnce(t.Context(), "dev-123")
	require.NoError(t, err)
	require.Equal(t, "authorization_pending", result.Error)
	require.Empty(t, result.RefreshToken)
}

func TestPollOnceReturnsTheIssuedToken(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, `{
			"refresh_token":"refresh-abc",
			"user_id":"user-1",
			"organization_id":"org-1",
			"organization_name":"Acme"
		}`)
	})

	result, err := pollOnce(t.Context(), "dev-123")
	require.NoError(t, err)
	require.Equal(t, "refresh-abc", result.RefreshToken)
	require.Equal(t, "user-1", result.UserID)
	require.Equal(t, "org-1", result.OrganizationID)
	require.Equal(t, "Acme", result.OrganizationName)
}

func TestPollOnceReportsANonOKStatus(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, `{"error":"bad_gateway"}`)
	})

	_, err := pollOnce(t.Context(), "dev-123")
	require.ErrorContains(t, err, "token request failed")
	require.ErrorContains(t, err, "status 502")
}

func TestPollOnceRejectsAMalformedBody(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, "<html>not json</html>")
	})

	_, err := pollOnce(t.Context(), "dev-123")
	require.ErrorContains(t, err, "unmarshal response")
}

func TestPollForTokenStopsWhenTheDeviceCodeExpires(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("an already-expired device code must not be polled")
	})

	_, err := PollForToken(t.Context(), "dev-123", 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExchangeTokenReturnsATokenWithAnExpiry(t *testing.T) {
	var body string
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/token/exchange", r.URL.Path)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body = string(raw)
		writeJSON(t, w, http.StatusOK, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600}`)
	})

	token, err := ExchangeToken(t.Context(), "refresh-1")
	require.NoError(t, err)
	require.Equal(t, "access-1", token.AccessToken)
	require.Equal(t, "refresh-1", token.RefreshToken)
	require.Contains(t, body, `"refresh_token":"refresh-1"`)
	require.InDelta(t, time.Now().Add(time.Hour).Unix(), token.ExpiresAt, 5)
}

func TestExchangeTokenReturnsATypedErrorOnFailure(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, `{"error":"invalid_grant"}`)
	})

	_, err := ExchangeToken(t.Context(), "stale")
	require.Error(t, err)

	var exchangeErr *oauth.TokenExchangeError
	require.True(t, errors.As(err, &exchangeErr), "callers need the typed error, got %T", err)
	require.Equal(t, http.StatusUnauthorized, exchangeErr.StatusCode)
	require.Contains(t, exchangeErr.Body, "invalid_grant")
}

func TestExchangeTokenRejectsAMalformedBody(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, "not json")
	})

	_, err := ExchangeToken(t.Context(), "refresh-1")
	require.ErrorContains(t, err, "unmarshal response")
}

func TestIntrospectTokenParsesTheResponse(t *testing.T) {
	var body string
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/token/introspect", r.URL.Path)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body = string(raw)

		writeJSON(t, w, http.StatusOK, `{"active":true,"sub":"user-1","org_id":"org-1","exp":42,"iat":1,"iss":"hyper","jti":"j1"}`)
	})

	resp, err := IntrospectToken(t.Context(), "access-1")
	require.NoError(t, err)
	require.True(t, resp.Active)
	require.Equal(t, "user-1", resp.Sub)
	require.Equal(t, "org-1", resp.OrgID)
	require.Equal(t, int64(42), resp.Exp)
	require.Contains(t, body, `"token":"access-1"`)
}

func TestIntrospectTokenReportsAnHTTPFailure(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, `nope`)
	})

	_, err := IntrospectToken(t.Context(), "access-1")
	require.ErrorContains(t, err, "token introspection failed")
	require.ErrorContains(t, err, "status 401")
}

func TestIntrospectTokenRejectsAMalformedBody(t *testing.T) {
	serveWith(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, "not json")
	})

	_, err := IntrospectToken(t.Context(), "access-1")
	require.ErrorContains(t, err, "unmarshal response")
}

func TestDeviceNameIsAttributedToCrush(t *testing.T) {
	name := deviceName()
	require.True(t, strings.HasPrefix(name, "Crush"), "got %q", name)

	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		require.Equal(t, "Crush ("+hostname+")", name)
	} else {
		require.Equal(t, "Crush", name)
	}
}
