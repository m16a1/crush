package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestQueries(t *testing.T) (*Queries, *sql.DB, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, Release(dataDir))
		ResetPool()
	})

	conn, err := Connect(t.Context(), dataDir)
	require.NoError(t, err)
	return New(conn), conn, dataDir
}

func mustCreateSession(t *testing.T, q *Queries, id, title string) Session {
	t.Helper()
	s, err := q.CreateSession(t.Context(), CreateSessionParams{
		ID:               id,
		Title:            title,
		PromptTokens:     100,
		CompletionTokens: 50,
		Cost:             1.25,
	})
	require.NoError(t, err)
	return s
}

func TestSessionQueries(t *testing.T) {
	q, _, _ := newTestQueries(t)

	created := mustCreateSession(t, q, "s1", "first")
	require.Equal(t, "first", created.Title)

	got, err := q.GetSessionByID(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "s1", got.ID)

	_, err = q.GetSessionByID(t.Context(), "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)

	last, err := q.GetLastSession(t.Context())
	require.NoError(t, err)
	require.Equal(t, "s1", last.ID)

	rows, err := q.RenameSession(t.Context(), RenameSessionParams{ID: "s1", Title: "renamed"})
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	renamed, err := q.GetSessionByID(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "renamed", renamed.Title)

	rows, err = q.UpdateSessionTitleAndUsage(t.Context(), UpdateSessionTitleAndUsageParams{
		ID: "s1", Title: "usage", PromptTokens: 7, CompletionTokens: 8, Cost: 0.5,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	updated, err := q.GetSessionByID(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "usage", updated.Title)

	channelled, err := q.SetSessionChannel(t.Context(), SetSessionChannelParams{
		ID: "s1", Channel: sql.NullString{String: "slack", Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "slack", channelled.Channel.String)

	full, err := q.UpdateSession(t.Context(), UpdateSessionParams{
		ID:               "s1",
		Title:            "full",
		PromptTokens:     1,
		CompletionTokens: 2,
		SummaryMessageID: sql.NullString{String: "sum", Valid: true},
		Cost:             3,
		Todos:            sql.NullString{String: `[]`, Valid: true},
		Channel:          sql.NullString{String: "slack", Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "full", full.Title)

	mustCreateSession(t, q, "s2", "second")
	sessions, err := q.ListSessions(t.Context())
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	require.NoError(t, q.DeleteSession(t.Context(), "s2"))
	sessions, err = q.ListSessions(t.Context())
	require.NoError(t, err)
	require.Len(t, sessions, 1)
}

func TestMessageQueries(t *testing.T) {
	q, _, _ := newTestQueries(t)
	mustCreateSession(t, q, "s1", "s")

	user, err := q.CreateMessage(t.Context(), CreateMessageParams{
		ID:        "m1",
		SessionID: "s1",
		Role:      "user",
		Parts:     `[{"type":"text","data":{"text":"hi"}}]`,
	})
	require.NoError(t, err)
	require.Equal(t, "m1", user.ID)

	assistant, err := q.CreateMessage(t.Context(), CreateMessageParams{
		ID:        "m2",
		SessionID: "s1",
		Role:      "assistant",
		Parts:     `[]`,
		Model:     sql.NullString{String: "gpt", Valid: true},
		Provider:  sql.NullString{String: "openai", Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "m2", assistant.ID)

	summary, err := q.CreateMessage(t.Context(), CreateMessageParams{
		ID:               "m3",
		SessionID:        "s1",
		Role:             "assistant",
		Parts:            `[]`,
		IsSummaryMessage: 1,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), summary.IsSummaryMessage)

	got, err := q.GetMessage(t.Context(), "m1")
	require.NoError(t, err)
	require.Equal(t, "user", got.Role)

	_, err = q.GetMessage(t.Context(), "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)

	all, err := q.ListMessagesBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, all, 3)

	users, err := q.ListUserMessagesBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, users, 1)

	everyUser, err := q.ListAllUserMessages(t.Context())
	require.NoError(t, err)
	require.Len(t, everyUser, 1)

	// Summary messages are excluded from this query.
	lastAssistant, err := q.GetLastAssistantMessageBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "m2", lastAssistant.ID)

	fromSummary, err := q.ListMessagesBySessionFromSummary(t.Context(), ListMessagesBySessionFromSummaryParams{
		SessionID: "s1", ID: "m3",
	})
	require.NoError(t, err)
	require.NotEmpty(t, fromSummary)

	require.NoError(t, q.UpdateMessage(t.Context(), UpdateMessageParams{
		ID:                      "m2",
		Parts:                   `[{"type":"text","data":{"text":"updated"}}]`,
		PrismModelID:            sql.NullString{String: "pm", Valid: true},
		PrismModelName:          sql.NullString{String: "prism", Valid: true},
		PrismHypercreditSavings: sql.NullFloat64{Float64: 0.1, Valid: true},
		PrismDollarSavings:      sql.NullFloat64{Float64: 0.2, Valid: true},
		FinishedAt:              sql.NullInt64{Int64: 1234, Valid: true},
	}))
	updated, err := q.GetMessage(t.Context(), "m2")
	require.NoError(t, err)
	require.Contains(t, updated.Parts, "updated")

	require.NoError(t, q.DeleteMessage(t.Context(), "m1"))
	remaining, err := q.ListMessagesBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, remaining, 2)

	require.NoError(t, q.DeleteSessionMessages(t.Context(), "s1"))
	remaining, err = q.ListMessagesBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Empty(t, remaining)
}

func TestFileQueries(t *testing.T) {
	q, _, _ := newTestQueries(t)
	mustCreateSession(t, q, "s1", "s")

	v1, err := q.CreateFile(t.Context(), CreateFileParams{
		ID: "f1", SessionID: "s1", Path: "/tmp/a.go", Content: "one", Version: 1,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), v1.Version)

	v2, err := q.CreateFile(t.Context(), CreateFileParams{
		ID: "f2", SessionID: "s1", Path: "/tmp/a.go", Content: "two", Version: 2,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), v2.Version)

	got, err := q.GetFile(t.Context(), "f1")
	require.NoError(t, err)
	require.Equal(t, "one", got.Content)

	byPath, err := q.GetFileByPathAndSession(t.Context(), GetFileByPathAndSessionParams{
		Path: "/tmp/a.go", SessionID: "s1",
	})
	require.NoError(t, err)
	require.Equal(t, "f2", byPath.ID)

	byPathList, err := q.ListFilesByPath(t.Context(), "/tmp/a.go")
	require.NoError(t, err)
	require.Len(t, byPathList, 2)

	bySession, err := q.ListFilesBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, bySession, 2)

	latest, err := q.ListLatestSessionFiles(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, latest, 1)
	require.Equal(t, "f2", latest[0].ID)

	require.NoError(t, q.DeleteFile(t.Context(), "f1"))
	byPathList, err = q.ListFilesByPath(t.Context(), "/tmp/a.go")
	require.NoError(t, err)
	require.Len(t, byPathList, 1)

	require.NoError(t, q.DeleteSessionFiles(t.Context(), "s1"))
	bySession, err = q.ListFilesBySession(t.Context(), "s1")
	require.NoError(t, err)
	require.Empty(t, bySession)
}

func TestReadFileQueries(t *testing.T) {
	q, _, _ := newTestQueries(t)
	mustCreateSession(t, q, "s1", "s")

	require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{
		SessionID: "s1", Path: "/tmp/read.go",
	}))

	read, err := q.GetFileRead(t.Context(), GetFileReadParams{SessionID: "s1", Path: "/tmp/read.go"})
	require.NoError(t, err)
	require.Equal(t, "/tmp/read.go", read.Path)

	all, err := q.ListSessionReadFiles(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestStatsQueries(t *testing.T) {
	q, _, _ := newTestQueries(t)

	s, err := q.CreateSession(t.Context(), CreateSessionParams{
		ID:               "s1",
		Title:            "stats",
		PromptTokens:     100,
		CompletionTokens: 40,
		Cost:             1.5,
	})
	require.NoError(t, err)
	require.Equal(t, "s1", s.ID)

	_, err = q.CreateMessage(t.Context(), CreateMessageParams{
		ID:        "m1",
		SessionID: "s1",
		Role:      "user",
		Parts:     `[]`,
		Model:     sql.NullString{String: "gpt", Valid: true},
		Provider:  sql.NullString{String: "openai", Valid: true},
	})
	require.NoError(t, err)

	_, err = q.CreateMessage(t.Context(), CreateMessageParams{
		ID:        "m2",
		SessionID: "s1",
		Role:      "assistant",
		Parts:     `[]`,
		Model:     sql.NullString{String: "gpt", Valid: true},
		Provider:  sql.NullString{String: "openai", Valid: true},
	})
	require.NoError(t, err)

	avg, err := q.GetAverageResponseTime(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, avg, int64(0))

	total, err := q.GetTotalStats(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, total.TotalSessions, int64(1))

	_, err = q.GetHourDayHeatmap(t.Context())
	require.NoError(t, err)

	_, err = q.GetRecentActivity(t.Context())
	require.NoError(t, err)

	usage, err := q.GetToolUsage(t.Context())
	require.NoError(t, err)
	require.NotNil(t, usage)

	_, err = q.GetUsageByDay(t.Context())
	require.NoError(t, err)

	_, err = q.GetUsageByDayOfWeek(t.Context())
	require.NoError(t, err)

	_, err = q.GetUsageByHour(t.Context())
	require.NoError(t, err)

	byModel, err := q.GetUsageByModel(t.Context())
	require.NoError(t, err)
	require.NotNil(t, byModel)

	// With no tool calls recorded, the usage list is empty.
	require.Empty(t, usage)
}

func TestCloseWithoutPreparedStatements(t *testing.T) {
	q, _, _ := newTestQueries(t)
	require.NoError(t, New(nil).Close())
	require.NoError(t, q.Close())
}

// TestPreparePreparedStatements guards the prepared-statement path: Prepare
// historically aborted because one of the queries it compiled referenced a
// column the schema never had.
func TestPreparePreparedStatements(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, Release(dataDir))
		ResetPool()
	})

	conn, err := Connect(t.Context(), dataDir)
	require.NoError(t, err)

	q, err := Prepare(t.Context(), conn)
	require.NoError(t, err)

	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "s1", Title: "prepared"})
	require.NoError(t, err)

	got, err := q.GetSessionByID(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "prepared", got.Title)

	require.NoError(t, q.Close())
}

func TestWithTxCommitAndRollback(t *testing.T) {
	q, conn, _ := newTestQueries(t)

	tx, err := conn.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	qtx := q.WithTx(tx)
	_, err = qtx.CreateSession(t.Context(), CreateSessionParams{ID: "committed", Title: "c"})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	got, err := q.GetSessionByID(t.Context(), "committed")
	require.NoError(t, err)
	require.Equal(t, "c", got.Title)

	tx, err = conn.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	qtx = q.WithTx(tx)
	_, err = qtx.CreateSession(t.Context(), CreateSessionParams{ID: "rolled-back", Title: "r"})
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	_, err = q.GetSessionByID(t.Context(), "rolled-back")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestConnectReadOnly(t *testing.T) {
	q, _, dataDir := newTestQueries(t)
	mustCreateSession(t, q, "s1", "readonly")

	conn, err := ConnectReadOnly(t.Context(), dataDir+"/crush.db")
	require.NoError(t, err)
	defer conn.Close()

	ro := New(conn)
	got, err := ro.GetSessionByID(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, "readonly", got.Title)

	// Writes must fail on a read-only connection.
	_, err = ro.CreateSession(t.Context(), CreateSessionParams{ID: "s2", Title: "nope"})
	require.Error(t, err)
}
