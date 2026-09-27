package oauth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTokenSetExpiresAt(t *testing.T) {
	t.Parallel()

	t.Run("uses expires_in when positive", func(t *testing.T) {
		t.Parallel()

		tok := &Token{ExpiresIn: 3600}
		tok.SetExpiresAt()

		require.InDelta(t, time.Now().Add(time.Hour).Unix(), tok.ExpiresAt, 5)
	})

	t.Run("keeps existing expires_at when expires_in is missing", func(t *testing.T) {
		t.Parallel()

		tok := &Token{ExpiresIn: 0, ExpiresAt: 12345}
		tok.SetExpiresAt()

		require.EqualValues(t, 12345, tok.ExpiresAt)
	})

	t.Run("marks as expired when no expiry information", func(t *testing.T) {
		t.Parallel()

		tok := &Token{ExpiresIn: -1}
		tok.SetExpiresAt()

		require.EqualValues(t, 0, tok.ExpiresAt)
	})
}

func TestTokenIsExpired(t *testing.T) {
	t.Parallel()

	t.Run("not expired within buffer", func(t *testing.T) {
		t.Parallel()

		// Buffer is max(1000/10, 30) == 100 seconds.
		tok := &Token{ExpiresIn: 1000, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
		require.False(t, tok.IsExpired())
	})

	t.Run("expired in the past", func(t *testing.T) {
		t.Parallel()

		tok := &Token{ExpiresIn: 1000, ExpiresAt: time.Now().Add(-time.Second).Unix()}
		require.True(t, tok.IsExpired())
	})

	t.Run("uses minimum buffer for short-lived tokens", func(t *testing.T) {
		t.Parallel()

		// ExpiresIn is 0, so buffer is the 30s minimum. A token expiring
		// in 10 seconds is already within the refresh window.
		tok := &Token{ExpiresIn: 0, ExpiresAt: time.Now().Add(10 * time.Second).Unix()}
		require.True(t, tok.IsExpired())
	})

	t.Run("zero expiry is expired", func(t *testing.T) {
		t.Parallel()

		tok := &Token{}
		require.True(t, tok.IsExpired())
	})
}

func TestTokenSetExpiresIn(t *testing.T) {
	t.Parallel()

	tok := &Token{ExpiresAt: time.Now().Add(100 * time.Second).Unix()}
	tok.SetExpiresIn()

	require.InDelta(t, 100, tok.ExpiresIn, 2)
}

func TestTokenExchangeError(t *testing.T) {
	t.Parallel()

	err := &TokenExchangeError{StatusCode: 400, Body: "bad request"}
	require.Contains(t, err.Error(), "status 400")
	require.Contains(t, err.Error(), "bad request")
	require.False(t, err.IsRefreshTokenRevoked())
}

func TestTokenExchangeErrorIsRefreshTokenRevoked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{"revoked", `{"error":"token revoked"}`, true},
		{"invalid_grant", `{"error":"invalid_grant"}`, true},
		{"other error", `{"error":"server_error"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := &TokenExchangeError{StatusCode: 400, Body: tt.body}
			require.Equal(t, tt.want, err.IsRefreshTokenRevoked())
		})
	}
}
