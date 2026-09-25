# Bugs and suspicious behaviour found while adding tests

These were found while raising test coverage. Bug 1 has since been fixed; the
rest are open, and none of the remaining ones is covered by a test. Each one is
recorded here instead so the behaviour stays visible without a green test suite
quietly encoding it as intended. Each entry lists the evidence needed to decide
what to do.

Status legend: **open** (needs a decision), **fixed** (no longer applies),
**dead code** (no live caller), **suspected** (behaviour may be intentional;
needs confirmation).

---

## 1. `db.Prepare` and `ListNewFiles` reference a column that does not exist

- **Status:** fixed. The dead query was deleted.
- **Location:**
  - `internal/db/sql/files.sql:58-62` — the query
  - `internal/db/db.go:117-119` — `Prepare` prepares it unconditionally
  - `internal/db/files.sql.go:251` — generated wrapper
  - `internal/db/querier.go:47` — interface entry
  - `internal/db/migrations/20250424200609_initial.sql:24-34` — the `files` table definition

### Evidence

The query filters on a column the schema never had:

```sql
-- internal/db/sql/files.sql:58
-- name: ListNewFiles :many
SELECT *
FROM files
WHERE is_new = 1
ORDER BY version DESC, created_at DESC;
```

```sql
-- internal/db/migrations/20250424200609_initial.sql:24
CREATE TABLE IF NOT EXISTS files (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    path TEXT NOT NULL,
    content TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE,
    UNIQUE(path, session_id, version)
);
```

There is no migration adding `is_new` anywhere under `internal/db/migrations/`.

`Prepare` builds every statement, so it aborts on this one. Observed directly
while writing `internal/db/queries_test.go`:

```
error preparing query ListNewFiles: SQL logic error: no such column: is_new (1)
```

Replacing the removed test with this reproduction confirms it:

```go
q, err := db.Prepare(ctx, conn)
// err == error preparing query ListNewFiles: SQL logic error: no such column: is_new (1)
```

### Blast radius

Neither `Prepare` nor `ListNewFiles` has a production caller today; everything
uses `db.New`. So nothing was broken in practice, but `Prepare` is the obvious
performance upgrade for the single-connection pool, and the moment it was
adopted the entire database layer would have failed to open. The query was also
permanently uncoverable, which capped `internal/db` coverage.

### Resolution

Deleted the dead query: removed it from `files.sql`, removed the generated
binding and interface entry by hand (no `sqlc` in the toolchain here), and
dropped its `Prepare`, `Close` and `WithTx` plumbing from `db.go`. `Prepare` now
works end to end and is covered by `TestPreparePreparedStatements` in
`internal/db/queries_test.go`; `internal/db` coverage rose from 59.7% to 73.0%.

---

## 2. `history.CreateVersion` shares one version counter across sessions

- **Status:** suspected.
- **Location:**
  - `internal/history/file.go:65-79` — `CreateVersion`
  - `internal/db/sql/files.sql:19-23` — `ListFilesByPath`
  - `internal/db/migrations/20250424200609_initial.sql:33` — the unique constraint

### Evidence

```go
// internal/history/file.go:65
func (s *service) CreateVersion(ctx context.Context, sessionID, path, content string) (File, error) {
	files, err := s.q.ListFilesByPath(ctx, path)
	...
	latestFile := files[0] // Files are ordered by version DESC, created_at DESC
	nextVersion := latestFile.Version + 1
```

`ListFilesByPath` is keyed on the path alone:

```sql
-- internal/db/sql/files.sql:19
-- name: ListFilesByPath :many
SELECT *
FROM files
WHERE path = ?
ORDER BY version DESC, created_at DESC;
```

There is no `session_id` filter, even though the table's uniqueness is
per-session: `UNIQUE(path, session_id, version)`. Two sessions that both touch
`/tmp/a.go` therefore draw from one shared counter: session B's first version
of the file comes out as whatever session A left off at, not `InitialVersion`.
Worse, if both sessions target the same path and land on the same computed
version, the insert collides with the unique constraint.

### Suggested fix

Make the version lookup session-scoped (add a `ListFilesByPathAndSession`
query, or reuse `GetFileByPathAndSession`), then compute `nextVersion` from
that. Needs a decision on whether cross-session version numbering is
deliberate — the constraint suggests it is not.

---

## 3. `question.Answer` / `Cancel` do not clear pending state eagerly

- **Status:** open. Latent panic / deadlock.
- **Location:** `internal/question/question.go:247-259` (`Ask` setup and teardown),
  `:275-294` (`Answer`), `:298-315` (`Cancel`)

### Evidence

`Ask` publishes a buffered channel and clears the fields only in a deferred
func that runs *after* the select returns:

```go
// internal/question/question.go:247
s.mu.Lock()
s.pending = make(chan []Answer, 1)
s.cancelled = make(chan struct{})
s.pendingID = req.ID
s.mu.Unlock()

defer func() {
	s.mu.Lock()
	s.pending = nil
	s.cancelled = nil
	s.pendingID = ""
	s.mu.Unlock()
}()
```

`Answer` and `Cancel` read the channels but never nil them:

```go
// internal/question/question.go:275
func (s *questionService) Answer(answers []Answer) bool {
	...
	ch := s.pending
	...
	if ch == nil {
		return false
	}
	ch <- answers          // buffered(1): a second call blocks forever
	...
}

// internal/question/question.go:298
func (s *questionService) Cancel() bool {
	...
	cancelCh := s.cancelled
	...
	if cancelCh == nil {
		return false
	}
	close(cancelCh)        // a second call panics: close of closed channel
	...
}
```

Consequences, all only reachable while `Ask` is still between its select and
its deferred cleanup:

- Two `Cancel` calls → `panic: close of closed channel`.
- Two `Answer` calls → the second blocks forever on the buffered channel.
- `Answer` followed by `Cancel` → `Cancel` returns `true` and fires a second
  notification even though the question is already resolved.

The TUI is the only caller and serialises these, so this is latent, not
currently triggered.

### Suggested fix

Resolve pending state inside `Answer`/`Cancel` under the existing mutex
(nil out `pending`, `cancelled` and `pendingID`), so the second caller sees
`nil` and returns `false`. `Ask`'s deferred cleanup stays as a backstop.

---

## 4. `getGitStatusSummary` reports "Status: clean" outside a git repo

- **Status:** open. Minor, cosmetic.
- **Location:** `internal/agent/prompt/prompt.go:271-281`
  (compare `:259-269` and `:283-290`)

### Evidence

```go
// internal/agent/prompt/prompt.go:271
func getGitStatusSummary(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git status --short 2>/dev/null | head -20")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "Status: clean\n", nil
	}
	return fmt.Sprintf("Status:\n%s\n", out), nil
}
```

The command is piped into `head`, so the shell reports `head`'s exit status, not
`git`'s. Outside a repository git exits non-zero, but the pipeline exits 0 and
`err` is nil, so the function falls through to the empty-output branch and
claims the tree is clean. The sibling helpers have no pipe and correctly
collapse to `""`:

```go
// internal/agent/prompt/prompt.go:259
out, _, err := sh.Exec(ctx, "git branch --show-current 2>/dev/null")
```

Observed while writing `internal/agent/prompt/prompt_test.go`: in a non-repo
temp dir, `getGitBranch` and `getGitRecentCommits` both returned `""`, while
`getGitStatusSummary` returned `"Status: clean\n"`.

### Blast radius

The system prompt for a non-repo project can assert the working tree is clean.
The same masking applies to a repo where `git status` itself fails.

### Suggested fix

Drop the `| head` pipe (truncate in Go instead) and let git's exit status
propagate, or have the caller only request a status when `isGitRepo` is true —
which `promptData` already does, so the fallback branch is still reachable if a
repo is removed mid-run.

---

## 5. Rename / usage updates on a missing session fail silently

- **Status:** open. Minor.
- **Location:**
  - `internal/db/sql/sessions.sql:62-70` and `:73-77`
  - `internal/session/session.go:248-273`, `:290-297`

### Evidence

Both statements are `:exec` updates with no row-count check:

```sql
-- internal/db/sql/sessions.sql:73
-- name: RenameSession :exec
UPDATE sessions
SET
    title = ?
WHERE id = ?;
```

An `UPDATE` that matches no rows is a successful statement, so
`service.Rename` and `service.UpdateTitleAndUsage` return `nil` for an unknown
session ID. The follow-up publish then swallows the real signal:

```go
// internal/session/session.go:290
func (s *service) publishSessionUpdate(ctx context.Context, sessionID string) {
	session, err := s.Get(ctx, sessionID)
	if err != nil {
		slog.Error("Failed to re-fetch session for event publish", ...)
		return
	}
	...
}
```

So a caller renaming a session that no longer exists gets success and no event.
Observed while writing `internal/session/crud_test.go`: both calls returned
`nil` for an ID that `Get` had already rejected with `sql.ErrNoRows`.

Note the concurrency angle: the re-fetch in `publishSessionUpdate` is the only
place the truth is checked, and by design it runs after the write, so a
session deleted between the update and the re-fetch is indistinguishable from
one that never existed.

### Suggested fix

Use `:execrows` (or `RETURNING *`) and map zero rows to a not-found error, then
decide whether callers should treat that as fatal. At minimum, surface it in
`publishSessionUpdate` rather than only logging.

---

## 6. `UpdateSessionTitleAndUsage` is cumulative, not absolute

- **Status:** suspected. Needs a caller audit.
- **Location:** `internal/db/sql/sessions.sql:62-70`

### Evidence

```sql
-- internal/db/sql/sessions.sql:62
-- name: UpdateSessionTitleAndUsage :exec
UPDATE sessions
SET
    title = ?,
    prompt_tokens = prompt_tokens + ?,
    completion_tokens = completion_tokens + ?,
    cost = cost + ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;
```

Despite the name, this adds to the existing values rather than setting them.
That is correct for a delta-based caller and silently double-counts for an
absolute-value caller. Observed while writing `internal/db/queries_test.go`: a
session created with 100 prompt tokens and updated with `7` came back at `107`.

### Suggested fix

Audit the callers to confirm every one passes deltas. If any passes a running
total, either rename the method to make the additive contract explicit
(e.g. `AddSessionUsage`) or split the title update from the usage update so
absolute writes are possible.
