# Bugs and suspicious behaviour found while adding tests

These were found while raising test coverage. Bugs 1, 2, 3 and 7a have since been
fixed and are covered by tests; the rest are open and are deliberately *not*
covered by tests. Each open one is recorded here instead, so the behaviour stays
visible without a green test suite quietly encoding it as intended. Each entry
lists the evidence needed to decide what to do.

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

## 2. `history.CreateVersion` shared one version counter across sessions

- **Status:** confirmed, but **not as originally described**. The version
  numbering defect was real and is now **fixed**; the claimed write failure never
  happened. The `ListLatestSessionFiles` row loss found while investigating is
  fixed too.
- **Location:**
  - `internal/history/file.go:66-86` — `CreateVersion`
  - `internal/db/sql/files.sql:19-23` — `ListFilesByPath`
  - `internal/db/sql/files.sql:47-56` — `ListLatestSessionFiles` (same root
    cause, worse symptom, but dead code)
  - `internal/db/migrations/20250424200609_initial.sql:33` — the unique constraint

### Evidence

```go
// internal/history/file.go:66, before the fix
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
`/tmp/a.go` therefore drew from one shared counter: session B's first version of
the file came out as whatever session A left off at, not `InitialVersion`.

**The claimed UNIQUE collision across sessions does not happen.** `(path, A, 2)`
and `(path, B, 2)` are different tuples, so both insert cleanly. Measured:

```
A Create        -> version=0
A CreateVersion -> version=1
B CreateVersion -> version=2      <- should be InitialVersion (0)
B owns: version=2
A owns: version=0, version=1
```

So the bleed was real but *cosmetic*, and nothing consumes the absolute value:
the only reader of `File.Version` is `internal/ui/model/session.go:138-147`,
which takes min/max **within the session** to pick the first and last content
for the diff, and the version number is never rendered
(`internal/ui/model/session.go:230-258` shows path + `+N/-N` only). Each
session's own sequence was still strictly increasing, so min/max still picked the
right pair and the displayed diff was correct.

Two consequences that *were* real:

1. **`ListLatestSessionFiles` dropped rows.** The same global-max pattern, but
   here the value is joined rather than merely incremented, so it filtered rows
   away:

   ```sql
   -- internal/db/sql/files.sql:50, before the fix
   INNER JOIN (
       SELECT path, MAX(version) as max_version, ... FROM files GROUP BY path
   ) latest ON f.path = latest.path AND f.version = latest.max_version ...
   WHERE f.session_id = ?
   ```

   The subquery had no session filter, so the global max version won. After the
   sequence above, `ListLatestSessionFiles("sess-A")` returned **0 rows**
   although sess-A owned 2 versions, while `("sess-B")` returned 1. A session-wide
   listing silently lost files. This is dead code today — nothing outside
   `internal/history/file.go:171` calls it — which is the only reason it was not
   user-visible.
2. **Every write read all versions of the path, contents included.** `SELECT *`
   with no limit and no session scope materialised the full content of every
   version of that path from every session, to obtain one integer. Cost grew with
   the project's edit history, not with the session's.

The retry loop in `createWithVersion` (`file.go:113-119`, `maxRetries = 3`) does
*not* rescue a collision either: it bumps with a blind `version++` and only
retries 3 times, so 4 concurrent same-session writers for one path leave one
writer failing with `UNIQUE constraint failed` (measured). That path is
unreachable in practice, though: `internal/agent/agent.go:660-722` holds a
per-session dispatch mutex and queues a second prompt rather than running it,
and tool calls within a turn are sequential, so a session never has two writers
in flight. Cross-session concurrency is fine because the tuples differ.

### Fix applied

`CreateVersion` now looks the previous version up through the existing
session-scoped `GetFileByPathAndSession` and treats `sql.ErrNoRows` as "no prior
version" (`file.go:66-86`), so numbering is per-session, only one row is read,
and session B starts at `InitialVersion`. `ListLatestSessionFiles` gained the
missing `WHERE session_id = ?` inside its subquery
(`internal/db/sql/files.sql:47-56`), and the hand-maintained generated code in
`internal/db/files.sql.go` was updated to match.

`ListFilesByPath` is now unused by production code. It is left in place: it is
not broken, it is still covered by `internal/db/queries_test.go`, and it is the
right query for a future cross-session view. The footgun was the *use* of it for
session-local numbering, not the query.

Covered by `TestCreateVersionCounterIsScopedToTheSession` and
`TestListLatestSessionFilesKeepsEverySessionThatSharesAPath`, both confirmed to
fail against the old code.

## 3. `question.Answer` / `Cancel` do not clear pending state eagerly

- **Status:** fixed. `Answer` and `Cancel` now claim the batch under the mutex.
- **Location:** `internal/question/question.go:223-278` (`Ask` setup and teardown),
  `:282-307` (`Answer`), `:311-336` (`Cancel`)

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

`Ask`'s deferred cleanup is the only thing that nils these fields, and it runs
*after* the select returns, so between the resolve and the cleanup every caller
still sees non-nil state.

### Reachability

The TUI alone cannot double-fire: `QuestionForm.HandleKey` returns `done = true`
on submit and on cancel, and `UI.Update` nils `activeInline` in that same
event-loop iteration (`internal/ui/model/ui.go:3182-3184`), so the next key is
never routed to the form.

But the TUI is **not the only caller**. `crush server`
(`internal/cmd/server.go`) registers `POST /v1/workspaces/{id}/questions/answer`
and `.../questions/cancel` (`internal/server/endpoints.go:275-290`), which call
straight through `internal/backend/question.go:11,34`, and `net/http` runs every
request on its own goroutine. Two clients — or one client that retries — can hit
these concurrently. Multiple answering clients are a *designed* scenario: the
notification broker exists so that "non-answering clients" can dismiss their open
forms. So this is reachable today, not merely latent.

### Confirmed with probes

Throwaway probes run against the pre-fix code (deleted afterwards; the repo is
clean):

| Probe | Setup | Observed |
|---|---|---|
| A | pending state populated, `Answer` twice | first `true`, second **blocked forever** |
| B | pending state populated, `Cancel` twice | first `true`, second **panicked: close of closed channel** |
| C | pending state populated, `Answer` then `Cancel` | `Cancel` returned `true`; **2 notifications** published |
| D | real pending `Ask` + two concurrent `Cancel` callers | **panic reproduced** on the 22nd iteration |

Probes A-C populate the pending fields directly so the deferred cleanup cannot
run, which isolates the missing state clearing. Probe D drives the real `Ask` and
shows the race is reachable rather than an artifact of the setup: the two callers
interleave between the check and the `close`.

### Fix

`Answer` and `Cancel` now inspect and claim the pending batch under the same
mutex hold: if a channel is nil they return `false`, otherwise they nil out
`pending`, `cancelled` and `pendingID` *before* releasing the lock, then do the
send/close and notification. That makes the check-and-claim atomic, so a second
caller — sequential or concurrent — sees no pending question and returns `false`.

`Ask` had to change too: it now captures its channels in locals and selects on
those, not on the struct fields. Clearing the fields in `Answer`/`Cancel` would
otherwise be able to unset the fields before a racing `Ask` reaches its select,
leaving it waiting on nil channels. The deferred cleanup stays as a backstop but
is now guarded by `s.pending == pending`, so a late cleanup cannot clobber a
newer batch.

Covered by `question_state_test.go`:
`TestAnswerTwiceResolvesTheBatchOnce`,
`TestCancelTwiceCancelsTheBatchOnce`,
`TestCancelAfterAnswerIsANoOp`,
`TestConcurrentCancelsResolveTheBatchOnce` and
`TestConcurrentAnswerAndCancelResolveTheBatchOnce`. All five were confirmed to
fail against the pre-fix code (block, panic, false positive, panic, double
resolve respectively). The package also passes under `-race`.

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
---

## 7. File-history versioning misbehaves on the first touch of a path

Two candidate defects with one cause: every tool that writes a file hand-rolls
its own "make sure the path has an initial version" step, and each does it
differently. Investigation split them, and only the first turned out to be real.

- **Status:** 7a confirmed and **fixed**. 7b checked and **not a bug** as
  originally written; the residual edge case is left alone.
- **Location:**
  - `internal/agent/tools/write.go:143-157`
  - `internal/agent/tools/edit.go:251-263`
  - `internal/agent/tools/multiedit.go:222-227`
  - `internal/history/file.go:65-82` (`CreateVersion`), `:84-135`
    (`createWithVersion`), `:58-60` (`Create`)

### 7a. `write` / `edit` stored two byte-identical versions on the first write

**Confirmed, fixed.** Both sites looked up the session's history, fell back to
`Create` when that missed, and then compared against the `File` returned by the
*failed* lookup:

```go
// internal/agent/tools/edit.go:251-263, before the fix
file, err := edit.files.GetByPathAndSession(edit.ctx, filePath, sessionID)
if err != nil {
    _, err = edit.files.Create(edit.ctx, sessionID, filePath, oldContent)
    if err != nil {
        return fmt.Errorf("error creating file history: %w", err)
    }
}
if file.Content != oldContent {   // <- file is the zero value here
    // User manually changed the content; store an intermediate version.
    if _, err := edit.files.CreateVersion(edit.ctx, sessionID, filePath, oldContent); err != nil {
        slog.Error("Error creating file history version", "error", err)
    }
}
```

`GetByPathAndSession` returns `history.File{}` alongside the error
(`file.go:145-154`), so `file.Content` was `""`, never the content the guard was
written to detect. For any non-empty file the "user manually changed the
content" branch fired on a path the session had never seen and wrote
`oldContent` a second time. Measured for one edit of a fresh path:

```
lookup miss leaves file.Content=""
branch taken: file.Content("") != oldContent("package main\n")
rows stored for one single edit: 3
  version=0 identicalOldContent=true  content="package main\n"
  version=1 identicalOldContent=true  content="package main\n"
  version=2 identicalOldContent=false content="package main\n\nfunc main() {}\n"
```

The displayed diff survived this, because `internal/ui/model/session.go:138-149`
diffs only the min and max versions and both were correct. The cost was a wasted
row per first edit, and the guard never did the job its comment claimed.

**Fix:** keep the `File` that `Create` returns and let the guard read it, in
both `write.go` and `edit.go`. `commitFileChange` in `edit.go` is also used by
`multiedit`'s existing-file path, so that path is fixed too. Covered by
`internal/agent/tools/history_versioning_test.go`, which asserts exactly one
stored version per content and was confirmed to fail against the old code.

### 7b. `multiedit` inserting an empty placeholder version — not a bug

**Checked, not a defect.** `processMultiEditWithCreation` does clear the slate
unconditionally:

```go
// internal/agent/tools/multiedit.go:221-227
_, err = edit.files.Create(edit.ctx, sessionID, params.FilePath, "")
_, err = edit.files.CreateVersion(edit.ctx, sessionID, params.FilePath, currentContent)
```

It is easy to read that as recording a bogus empty version. It is not: this
branch only runs when the file does **not** exist on disk — `multiedit` returns
"file already exists" otherwise (`multiedit.go:156-160`) and edits go through
`commitFileChange` instead. For a file being created, `""` is the correct
baseline, and the resulting first-vs-last diff reporting the whole file as added
matches what the permission prompt already reports
(`multiedit.go:177` diffs `""` against `currentContent`). No fix needed.

One residual edge case, left as is: if a path was created in a session and then
deleted, recreating it makes `Create`'s fixed version 0 collide, so the retry in
`createWithVersion` (`file.go:113-119`) silently lands the empty baseline on a
higher version and the session's first version for that path stays the old
deleted content. `Create` therefore does not strictly honour `InitialVersion` in
that case. It is a contrived scenario, the diff is only mildly wrong, and the
retry is load-bearing (without it `Create` would fail outright), so it is
documented rather than changed.
