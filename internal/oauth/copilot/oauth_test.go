package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// withEndpoints points the three hardcoded GitHub URLs at a test server for
// the duration of a test and restores them afterwards. Tests using it must
// not run in parallel.
func withEndpoints(t *testing.T, base string) {
	t.Helper()
	device, access, copilotURL := deviceCodeURL, accessTokenURL, copilotTokenURL
	t.Cleanup(func() {
		deviceCodeURL, accessTokenURL, copilotTokenURL = device, access, copilotURL
	})
	deviceCodeURL = base + "/device"
	accessTokenURL = base + "/access"
	copilotTokenURL = base + "/copilot"
}

func TestRequestDeviceCode(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Accept"))
			require.Equal(t, userAgent, r.Header.Get("User-Agent"))
			require.NoError(t, r.ParseForm())
			require.Equal(t, clientID, r.FormValue("client_id"))

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"device_code":"d","user_code":"u","verification_uri":"v","expires_in":900,"interval":5}`)
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		dc, err := RequestDeviceCode(t.Context())
		require.NoError(t, err)
		require.Equal(t, "d", dc.DeviceCode)
		require.Equal(t, "u", dc.UserCode)
		require.Equal(t, 900, dc.ExpiresIn)
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "nope")
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := RequestDeviceCode(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "device code request failed")
	})

	t.Run("bad json is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "not json")
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := RequestDeviceCode(t.Context())
		require.Error(t, err)
	})
}

func TestTryGetToken(t *testing.T) {
	t.Run("authorization pending", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := tryGetToken(t.Context(), "dc")
		require.ErrorIs(t, err, errPending)
	})

	t.Run("slow down", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"error":"slow_down"}`)
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := tryGetToken(t.Context(), "dc")
		require.ErrorIs(t, err, errSlowDown)
	})

	t.Run("empty token without error is still pending", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{}`)
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := tryGetToken(t.Context(), "dc")
		require.ErrorIs(t, err, errPending)
	})

	t.Run("other errors surface", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"error":"access_denied"}`)
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := tryGetToken(t.Context(), "dc")
		require.Error(t, err)
		require.Contains(t, err.Error(), "authorization failed: access_denied")
	})

	t.Run("bad json is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "nope")
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := tryGetToken(t.Context(), "dc")
		require.Error(t, err)
	})

	t.Run("success exchanges the github token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/access":
				fmt.Fprint(w, `{"access_token":"gh-token"}`)
			case "/copilot":
				require.Equal(t, "Bearer gh-token", r.Header.Get("Authorization"))
				require.Equal(t, editorVersion, r.Header.Get("Editor-Version"))
				fmt.Fprint(w, `{"token":"cop-token","expires_at":4102444800}`)
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		token, err := tryGetToken(t.Context(), "dc")
		require.NoError(t, err)
		require.Equal(t, "cop-token", token.AccessToken)
		require.Equal(t, "gh-token", token.RefreshToken)
		require.Equal(t, int64(4102444800), token.ExpiresAt)
		require.Positive(t, token.ExpiresIn)
	})
}

func TestGetCopilotTokenErrors(t *testing.T) {
	t.Run("forbidden means unavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "no copilot for you")
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := getCopilotToken(t.Context(), "gh")
		require.ErrorIs(t, err, ErrNotAvailable)
	})

	t.Run("other status codes are errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "boom")
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := getCopilotToken(t.Context(), "gh")
		require.Error(t, err)
		require.Contains(t, err.Error(), "copilot token request failed")
	})

	t.Run("bad json is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "nope")
		}))
		defer server.Close()
		withEndpoints(t, server.URL)

		_, err := getCopilotToken(t.Context(), "gh")
		require.Error(t, err)
	})
}

func TestRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"cop","expires_at":4102444800}`)
	}))
	defer server.Close()
	withEndpoints(t, server.URL)

	token, err := RefreshToken(t.Context(), "gh")
	require.NoError(t, err)
	require.Equal(t, "cop", token.AccessToken)
	require.Equal(t, "gh", token.RefreshToken)
}

func TestPollForTokenTimesOutImmediately(t *testing.T) {
	withEndpoints(t, "http://127.0.0.1:1")

	_, err := PollForToken(t.Context(), &DeviceCode{DeviceCode: "d", ExpiresIn: 0, Interval: 5})
	require.Error(t, err)
	require.Contains(t, err.Error(), "authorization timed out")
}

func TestPollForTokenHonorsContextCancellation(t *testing.T) {
	withEndpoints(t, "http://127.0.0.1:1")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := PollForToken(ctx, &DeviceCode{DeviceCode: "d", ExpiresIn: 900, Interval: 5})
	require.ErrorIs(t, err, context.Canceled)
}

func TestPollForTokenSucceedsAfterAPendingPoll(t *testing.T) {
	var polls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"gh-token"}`)
	}))
	defer server.Close()

	// The device flow enforces a minimum poll interval of five seconds, so
	// this test intentionally waits one interval before succeeding.
	withEndpoints(t, server.URL)

	start := time.Now()
	token, err := PollForToken(t.Context(), &DeviceCode{DeviceCode: "d", ExpiresIn: 900, Interval: 5})
	require.NoError(t, err)
	require.Equal(t, "gh-token", token.RefreshToken)
	require.GreaterOrEqual(t, time.Since(start), 5*time.Second)
}

func TestRefreshTokenFromDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "github-copilot"), 0o755))

	t.Run("missing file", func(t *testing.T) {
		token, ok := RefreshTokenFromDisk()
		require.False(t, ok)
		require.Empty(t, token)
	})

	t.Run("bad json", func(t *testing.T) {
		path := tokenFilePath()
		require.NoError(t, os.WriteFile(path, []byte("nope"), 0o644))
		t.Cleanup(func() { _ = os.Remove(path) })

		token, ok := RefreshTokenFromDisk()
		require.False(t, ok)
		require.Empty(t, token)
	})

	t.Run("known app entry", func(t *testing.T) {
		path := tokenFilePath()
		payload := map[string]any{
			"github.com:Iv1.b507a08c87ecfe98": map[string]string{
				"user":        "octocat",
				"oauth_token": "gh-token",
			},
		}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o644))
		t.Cleanup(func() { _ = os.Remove(path) })

		token, ok := RefreshTokenFromDisk()
		require.True(t, ok)
		require.Equal(t, "gh-token", token)
	})

	t.Run("unrelated app entry", func(t *testing.T) {
		path := tokenFilePath()
		require.NoError(t, os.WriteFile(path, []byte(`{"other":{"oauth_token":"x"}}`), 0o644))
		t.Cleanup(func() { _ = os.Remove(path) })

		token, ok := RefreshTokenFromDisk()
		require.False(t, ok)
		require.Empty(t, token)
	})
}

func TestHeaders(t *testing.T) {
	t.Parallel()

	headers := Headers()
	require.Equal(t, userAgent, headers["User-Agent"])
	require.Equal(t, editorVersion, headers["Editor-Version"])
	require.Equal(t, editorPluginVersion, headers["Editor-Plugin-Version"])
	require.Equal(t, integrationID, headers["Copilot-Integration-Id"])
}

func TestNewClientRoundTripThroughBaseTransport(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "initiator=%s", r.Header.Get("X-Initiator"))
	}))
	defer server.Close()

	client := NewClient(false, false, http.DefaultTransport.(*http.Transport))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var buf [64]byte
	n, _ := resp.Body.Read(buf[:])
	require.Equal(t, "initiator=user", string(buf[:n]))
}

func TestNewClientDebugWrapsRoundTripper(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	client := NewClient(false, true, http.DefaultTransport.(*http.Transport))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
}

func TestInitiatorTransportNilBaseNilRequest(t *testing.T) {
	t.Parallel()

	tr := &initiatorTransport{}
	_, err := tr.RoundTrip(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil")
}
