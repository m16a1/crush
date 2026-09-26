package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateTitleAndUsageLeavesTheTokenGaugesAlone(t *testing.T) {

	svc := newTestService(t)

	s, err := svc.Create(t.Context(), "gauge")
	require.NoError(t, err)

	// A turn records the context gauge: the size of the prompt the provider
	// processed and the output it produced.
	s.PromptTokens = 100
	s.CompletionTokens = 20
	_, err = svc.Save(t.Context(), s)
	require.NoError(t, err)

	// A title (re)generation adds its cost, which is a real charge, but its
	// prompt is the title request, not the session's context, so the gauges
	// must not move.
	require.NoError(t, svc.UpdateTitleAndUsage(t.Context(), s.ID, "Retitled", 0.002))

	got, err := svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	require.Equal(t, "Retitled", got.Title)
	require.Equal(t, int64(100), got.PromptTokens)
	require.Equal(t, int64(20), got.CompletionTokens)
	require.InDelta(t, 0.002, got.Cost, 1e-9)
}
