package httperror

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"
)

func TestNormalizeBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		status  int
		want    string
		changed bool
	}{
		{
			name:    "string error from an openai compatible server",
			body:    `{"error":"invalid api key"}`,
			status:  http.StatusUnauthorized,
			want:    `{"error":{"message":"invalid api key"}}`,
			changed: true,
		},
		{
			name:    "openai error envelope is kept as it is",
			body:    `{"error":{"message":"nope","type":"invalid_request_error","code":"x","param":null}}`,
			status:  http.StatusBadRequest,
			want:    `{"error":{"message":"nope","type":"invalid_request_error","code":"x","param":null}}`,
			changed: false,
		},
		{
			name:    "object without an error field keeps its other fields",
			body:    `{"message":"context length exceeded","type":"error"}`,
			status:  http.StatusBadRequest,
			want:    `{"error":{"message":"context length exceeded"},"message":"context length exceeded","type":"error"}`,
			changed: true,
		},
		{
			name:    "bare json string",
			body:    `"rate limited"`,
			status:  http.StatusTooManyRequests,
			want:    `{"error":{"message":"rate limited"}}`,
			changed: true,
		},
		{
			name:    "html served by a gateway",
			body:    "<html>\n  <body>502 Bad Gateway</body>\n</html>",
			status:  http.StatusBadGateway,
			want:    `{"error":{"message":"<html> <body>502 Bad Gateway</body> </html>"}}`,
			changed: true,
		},
		{
			name:    "empty body",
			body:    "",
			status:  http.StatusForbidden,
			want:    `{"error":{"message":"the provider returned HTTP 403 (Forbidden) with an empty response body"}}`,
			changed: true,
		},
		{
			name:    "null error",
			body:    `{"error":null}`,
			status:  http.StatusInternalServerError,
			want:    `{"error":{"message":"{\"error\":null}"}}`,
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, changed := NormalizeBody([]byte(tt.body), tt.status)
			require.Equal(t, tt.changed, changed)
			require.Equal(t, tt.want, string(got))
		})
	}
}

func TestNormalizeBodyTruncatesHugeBodies(t *testing.T) {
	t.Parallel()

	got, changed := NormalizeBody([]byte(strings.Repeat("a", maxMessageLength*2)), http.StatusBadGateway)
	require.True(t, changed)

	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(got, &envelope))
	require.True(t, strings.HasSuffix(envelope.Error.Message, truncatedSuffix))
	require.Len(t, []rune(envelope.Error.Message), maxMessageLength+1)
}

// stubBody stands in for a response body so tests can tell whether it was
// replaced or closed.
type stubBody struct {
	io.Reader
	closed bool
}

func (b *stubBody) Close() error {
	b.closed = true
	return nil
}

type stubTransport struct {
	resp *http.Response
}

func (t *stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return t.resp, nil
}

func TestTransportLeavesSuccessfulResponsesUntouched(t *testing.T) {
	t.Parallel()

	body := &stubBody{Reader: strings.NewReader(`{"ok":true}`)}
	resp := &http.Response{StatusCode: http.StatusOK, Body: body, Header: http.Header{}}

	got, err := Transport(&stubTransport{resp: resp}).RoundTrip(httptest.NewRequest(http.MethodPost, "/", nil))
	require.NoError(t, err)
	require.Same(t, resp, got)
	require.Same(t, body, got.Body, "successful responses must stream through untouched")
	require.False(t, body.closed)
}

func TestTransportSkipsCompressedBodies(t *testing.T) {
	t.Parallel()

	body := &stubBody{Reader: strings.NewReader("not really gzipped")}
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       body,
		Header:     http.Header{"Content-Encoding": []string{"gzip"}},
	}

	got, err := Transport(&stubTransport{resp: resp}).RoundTrip(httptest.NewRequest(http.MethodPost, "/", nil))
	require.NoError(t, err)
	require.Same(t, body, got.Body, "a still-compressed body cannot be rewritten")
	require.False(t, body.closed)
}

func TestTransportNormalizesErrorResponses(t *testing.T) {
	t.Parallel()

	body := &stubBody{Reader: strings.NewReader(`{"error":"invalid api key"}`)}
	resp := &http.Response{StatusCode: http.StatusUnauthorized, Body: body, Header: http.Header{}}

	got, err := Transport(&stubTransport{resp: resp}).RoundTrip(httptest.NewRequest(http.MethodPost, "/", nil))
	require.NoError(t, err)
	require.True(t, body.closed, "the original body is consumed and replaced")

	raw, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"error":{"message":"invalid api key"}}`, string(raw))
	require.Equal(t, int64(len(raw)), got.ContentLength)
	require.Equal(t, strconv.Itoa(len(raw)), got.Header.Get("Content-Length"))
}

func TestClientNormalizesRealErrorResponses(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid api key"}`)
	}))
	t.Cleanup(srv.Close)

	resp, err := WithNormalizedErrors(&http.Client{}).Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"error":{"message":"invalid api key"}}`, string(raw))
}

// TestProviderErrorsSurviveNormalization is the end to end check: a provider
// that answers a failure with a string error must reach the caller as the
// provider's own message, without the JSON wrapper the SDK would otherwise
// report verbatim.
func TestProviderErrorsSurviveNormalization(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid api key"}`)
	}))
	t.Cleanup(srv.Close)

	t.Run("normalized error keeps its status and message", func(t *testing.T) {
		err := generateAgainst(t, srv.URL, WithNormalizedErrors(&http.Client{}))

		var providerErr *fantasy.ProviderError
		require.True(t, errors.As(err, &providerErr), "expected a provider error, got %T: %v", err, err)
		require.Equal(t, http.StatusUnauthorized, providerErr.StatusCode)
		require.Equal(t, "invalid api key", providerErr.Message)
		require.NotEmpty(t, providerErr.Title)
	})

	t.Run("unnormalized error buries the message in its own JSON", func(t *testing.T) {
		err := generateAgainst(t, srv.URL, &http.Client{})

		var providerErr *fantasy.ProviderError
		require.True(t, errors.As(err, &providerErr), "the SDK still classifies the error, got %T: %v", err, err)
		require.Equal(t, http.StatusUnauthorized, providerErr.StatusCode)
		require.NotEqual(t, "invalid api key", providerErr.Message,
			"the raw body, not the provider's message, is what reaches the caller")
		require.Contains(t, providerErr.Message, `{"error":"invalid api key"}`)
	})
}

// generateAgainst makes a chat completion request against url using an
// OpenAI-compatible provider, and returns the resulting error.
func generateAgainst(t *testing.T, url string, client *http.Client) error {
	t.Helper()

	provider, err := openaicompat.New(
		openaicompat.WithBaseURL(url),
		openaicompat.WithAPIKey("test"),
		openaicompat.WithHTTPClient(client),
	)
	require.NoError(t, err)

	model, err := provider.LanguageModel(context.Background(), "test-model")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = model.Generate(ctx, fantasy.Call{Prompt: fantasy.Prompt{fantasy.NewUserMessage("hi")}})
	require.Error(t, err)
	return err
}
