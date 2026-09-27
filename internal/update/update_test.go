package update

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckForUpdate_Old(t *testing.T) {
	info, err := Check(t.Context(), "v0.10.0", testClient{"v0.11.0"})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.True(t, info.Available())
}

func TestCheckForUpdate_Beta(t *testing.T) {
	t.Run("current is stable", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.10.0", testClient{"v0.11.0-beta.1"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.False(t, info.Available())
	})

	t.Run("current is also beta", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", testClient{"v0.11.0-beta.2"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})

	t.Run("current is beta, latest isn't", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", testClient{"v0.11.0"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})
}

func TestInfoIsDevelopment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		want    bool
	}{
		{"devel", "devel", true},
		{"unknown", "unknown", true},
		{"dirty", "v1.2.3-dirty", true},
		{"go install pseudo version", "v0.0.0-0.20251231235959-06c807842604", true},
		{"tagged release", "v1.2.3", false},
		{"pre-release", "v1.2.3-beta.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, Info{Current: tt.current}.IsDevelopment())
		})
	}
}

func TestCheckError(t *testing.T) {
	t.Parallel()

	_, err := Check(t.Context(), "v0.10.0", failingClient{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch latest release")
}

type testClient struct{ tag string }

// Latest implements Client.
func (t testClient) Latest(ctx context.Context) (*Release, error) {
	return &Release{
		TagName: t.tag,
		HTMLURL: "https://example.org",
	}, nil
}

type failingClient struct{}

// Latest implements Client.
func (failingClient) Latest(context.Context) (*Release, error) {
	return nil, errors.New("network error")
}
