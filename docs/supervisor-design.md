# Supervisor + Mastermind: parallel feature orchestration for Crush

Design plan for running a multi-feature epic (A -> B -> C -> D) from one
prompt: the user describes the work, presses enter, and walks away. The
system implements all of it, survives errors and context overflow, and
stays inspectable the whole time.

## Implementation status

Implemented (2026-10-16), gated behind `option orchestration true`:

- `context_window` override on selected models (`model large ... --context-window N`).
- `options.max_parallel_subagents` (`option max-parallel-subagents N`, default 2).
- Supervisor actor (`internal/agent/supervisor.go`): DAG, slot semaphore,
  priority scheduling, restart budgets, watchdogs, verify_cmd evidence,
  deadlock and budget detection, debounced event reports.
- Persistence (`internal/agent/planstore.go`): one JSON document per
  mastermind session under `<datadir>/plans/`, atomically rewritten;
  running/verifying nodes are returned to pending on restore. There is
  deliberately no automatic rescheduling on restart: a restored plan
  waits for the mastermind's next plan-tool action to relaunch.
- Mastermind tools (`internal/agent/plan_tool.go`): single `plan` tool
  with actions add/update/retry/remove/pause/resume/set_limit/status.
- Async worker children (`internal/agent/orchestration.go`): coder-,
  task-, or supervisor-toolset workers in real child sessions (titled
  `plan: <node>`), cost rolled up to the parent session; wake-ups ride
  the background-job report -> interrupt/queue path; every wake-up
  report re-injects the full plan rendering.
- Supervision tree: a node with `agent=supervisor` authors and runs its
  own sub-plan (depth capped at 2 below the mastermind) and completes
  only when that sub-plan completes. Pausing or removing a supervisor
  node pauses its whole subtree. Each deeper level must receive a
  strictly simpler task - enforced by doctrine and by the depth cap.
- Plan screen (TUI, `ctrl+e` or the command palette): full-screen view
  of the current session's plan - node states, attempts, budgets, and
  per-worker statistics (session cost and average output tokens per
  second, computed from message history). Enter on a node opens its
  worker session. Subagent sessions no longer appear in the sessions
  popup; the plan screen is their front door.
- Verifier children: read-only task-agent children judging
  `verify_criteria` against claim + evidence, JSON verdict contract.
- Mastermind turn-death recovery: the Supervisor notices structurally
  (via `noteMainRunEnd`) and restarts the turn with the plan state, up
  to 3 times before escalating once.
- Coder system prompt gains an `<orchestration>` section when enabled,
  biasing the mastermind toward delegating multi-part work to subagents.
The Supervisor actor itself is provider-free and covered by
`internal/agent/supervisor_test.go`; the plan screen's statistics are
covered by `internal/ui/dialog/plan_test.go`.

## Problems this fixes

1. **Forgetting.** Across auto-summarization only the raw original prompt
   survives (agent.go, the "previous session was interrupted" requeue).
   There is no durable per-feature state, and the model grades its own
   progress from memory.
2. **One error kills the run.** `Run` returns the first provider/stream
   error and the turn ends (`return nil, err` in `sessionAgent.Run`);
   nothing respawns it, so remaining features die with it.
3. **Bogus context windows.** `ContextWindow` comes solely from the
   catwalk catalog (config.go:202). `ModelCfg` has no `context_window`
   override, so a locally served model with an unknown or wrong catalog
   value (e.g. Qwen Coder Flash Next) gets auto-summarize silently
   skipped (agent.go:1256-1271) and is treated as infinite.
4. **Opaque subagents.** Subagent runs are already real persisted sessions
   (`CreateTaskSession`, coordinator.go:1776), but the TUI never lets you
   open a child session and watch it live.
5. **The todo tool is gone** (removed as always-out-of-track); a supervisor
   that watches but does not do is the replacement.

## Core split: mechanism vs policy

Two roles, modeled on Erlang supervision:

- **Supervisor** — a pure Go actor living in the coordinator. No LLM.
  Owns all state and all scheduling. Testable without a provider.
- **Mastermind** — the main coder session (LLM + harness + new tools).
  Owns plan authorship and judgment. Alive only during turns; everything
  it must remember is re-injected by the Supervisor.

The Mastermind's own turn is just another child process under the
Supervisor: if the main stream dies on a provider hiccup, the Supervisor
(notices structurally, being code) restarts the turn and hands the
Mastermind a fresh rendering of the plan. "It died and nothing continued"
becomes structurally impossible.

## Supervisor (algorithm)

State: a persisted DAG of plan nodes plus a slot table.

**Node states:** `pending` (deps unsatisfied) -> `ready` (deps verified,
slot free) -> `running` -> `verified` / `failed`, plus `stalled`
(finished but verification failed, needs adjudication). `ready` is
computed by code from DAG edges + free slots, never from LLM memory.

**Owns:**

- Node lifecycle: `start`, `stop`, `kill`, `restart` (continue = resume
  the child session with its context intact; restart = fresh session,
  same prompt).
- Slot semaphore at `max_parallel` (given per-plan by the Mastermind,
  clamped by the config option). The Mastermind is never asked "may I
  launch node X"; policy is set once, the scheduler executes it.
- Watchdogs: stream stalls, provider errors, timeouts. Restart within
  budget, serialize when the provider has one request slot.
- Scheduling decisions: what is ready, what to launch, when, in what
  order. Order comes from metadata the Mastermind baked into the plan:
  `priority`, `depends_on`, per-node prompt, `verify_cmd`.
- Evidence collection: runs a node's `verify_cmd`, captures exit codes
  and diffs. Evidence is an algorithm; judgment is not.
- Deadlock detection: no running, no ready, some nodes unfinished is
  decidable in O(nodes) by code; resolution is an escalation.

**Emits events** to the Mastermind (as report turns, reusing the
background-job report -> interrupt -> queue path): `node_finished`
(a claim, never a transition), `verify_green`, `watchdog_timeout`,
`budget_exhausted`, `deadlock`, `slot_freed`.

**Two distinct events matter for scheduling:**

- A node *finished* (claim) frees its slot for any new node.
- A node *verified done* unlocks its dependents.

So slot-fill can proceed while dependents still wait on real verification.

## Mastermind (LLM + harness)

Wakes only for genuine judgment:

- Verdict ambiguous (the subagent's claim disagrees with the evidence).
- Restart budget exhausted: continue-amended vs kill-and-restart vs mark failed.
- Deadlock: insert the missing prerequisite node, rewire an edge.
- Plan amendments in general (add node, reprioritize, pause).
- Authorship: creates the DAG once at epic start, sets `max_parallel`.

It influences dispatch by **editing the plan**, never by micromanaging
starts. Commands mutate policy; they do not poke the scheduler loop.

**Verification is two-tier:**

- Cheap: `verify_cmd` exit code, collected by the Supervisor.
- Expensive: read-only verifier child, given the diff + acceptance
  criteria, returns a structured verdict (same JSON-contract pattern as
  the grammar sidecar in internal/agent/grammar.go).

The subagent's completion report is a claim; status transitions only on
verified evidence. A claim can be checked by the Mastermind, found true
but insufficient ("done, but the part needed to start D is missing"), and
answered by inserting an emergency node before D and letting the
scheduler pick it up.

Every Mastermind wake-up prompt carries a fresh rendering of Supervisor
state: plan table, slot table (`running 2/2: [a, b] - ready: [c, d, e] -
done: [] - budget: 3 restarts left`), and the triggering event.
Post-compaction amnesia becomes harmless: the worst case is a redundant
question, never a lost feature.

## Protocol

- Supervisor -> Mastermind: events (report turns with state rendering).
- Mastermind -> Supervisor: commands via tools: `plan_add`,
  `plan_update`, `reprioritize`, `add_node`, `pause`/`resume`,
  `set_limit`, `start`/`stop`/`restart` (as policy-level intents).
- Refusal is normal: slots full or budget gone returns a tool error,
  the same way any other truth reaches the model from the runtime.
- Worker subagents and verifier children are the same kind of child to
  the Supervisor; roles are just the prompt/harness a child started with.

## Subagents become real

- New params on the orchestration tool: `agent=coder|task` (writable
  toolset for coder-type workers; today the task agent is read-only,
  coordinator.go:1900), `async=true` (a non-blocking variant of
  `runSubAgent`, coordinator.go:1776, that does not await `Run`),
  `verify_cmd`, `depends_on`, `priority`.
- Child sessions are real sessions with deterministic IDs
  (`CreateAgentToolSessionID(agentMessageID, toolCallID)`), so the chat
  renderer can recompute a child ID from the tool block itself; no extra
  plumbing needed for the UI.
- Guards: restart budget per node, spend cap per plan, disjoint-files
  rule for concurrently writable subagents (two coders on one file is
  how orchestrations die).
- Provider reality check: 1-concurrent-request models serialize "parallel"
  subagents server-side. The architecture still pays off in context
  isolation, but no wall-clock speedup there.

## Configuration

- `options.max_parallel_subagents` (default 2), exposed in crushrc via
  one line in `shellconfig/options.go` `optionSpecs`. Per-plan value can
  only lower it.
- `context_window` override on `ModelCfg` (config.go:82-92 area) plus
  the crushrc model block, so auto-summarize thresholds can be corrected
  for locally served models. Trivial fix, independent of everything else.

## UI

Implemented surfaces:

1. **Plan screen** (`ctrl+e` / command palette): the current session's
   plan rendered full-screen - node badges, states, attempts, budgets,
   and per-worker stats (cost, avg output tokens/sec). Enter on a node
   switches into its worker session for live inspection; workers are
   real sessions, so message streaming makes the view live.
2. **Sessions popup**: subagent sessions (parented sessions) are
   filtered out; the plan screen is their entry point.

Deferred:

- In-chat peek view that opens a worker session without leaving the
  conversation (opencode parity). Typing into a peeked subagent later.
- Hierarchical session manager grouping children under parents.

The existing nested rendering in the chat (agent tool blocks with
embedded child tools) stays as the collapsed summary; the peek is for
depth.

**Keys** (all real chords checked against internal/ui/keys.go:94-256;
`enter` belongs to the editor, `esc` means cancel-agent; ctrl+{c,d,f,g,
j,k,m,n,o,p,r,s,t,x,y,z,b}, tab, shift+tab, arrows, j/k/J/K, `/`, `@`
are taken):

| Action | Chord |
|---|---|
| Open focused subagent block | `alt+enter` (fallback `ctrl+e`) |
| Close peek | `q` (modal-view convention) |
| Cycle running children in peek | `[` / `]` |
| Fleet overview | reuse `ctrl+s`, made hierarchical |

"Focused block" reuses the existing chat item navigation
(shift+up / shift+down, keys.go:251-256); no new navigation model.

## Build order

1. `context_window` override in `ModelCfg` + crushrc. Trivial, unblocks
   correct auto-summarize today.
2. Async subagents + report wiring + child-session peek view (fixes
   opacity and one-error-death at the subagent level).
3. Plan DAG persistence + Supervisor actor + Mastermind tools.
4. Verifier child pattern (grammar-sidecar precedent).
5. Plan panel + hierarchical session manager + slot/budget rendering.

Each step is independently shippable behind the `options` flags.
