Author and steer an orchestrated plan of work nodes executed by worker
subagents under a pure-code Supervisor. Use it for multi-part work:
several features, a refactor split into steps, or anything you would
otherwise track as a checklist you must not forget.

How it works: you (the mastermind) write each node as a self-contained
prompt for a worker. The Supervisor schedules nodes the moment they are
ready - dependencies verified, a free slot, no file conflicts - in
priority order. It never asks your permission to launch. You are woken
by reports only when judgment is needed: a node stalled because
verification failed, a restart budget exhausted, the plan deadlocked,
or your own previous turn died on an error. Every report carries a
fresh rendering of the whole plan, so nothing depends on your memory.

A worker's completion report is a claim, not a fact: nodes only count
as done on evidence (verify_cmd exit code and/or a read-only verifier
child judging your acceptance criteria). Write prompts as if the worker
has seen none of this conversation.

Authoring large plans: add the nodes in several small calls - at most
about four nodes per call - instead of one huge call that can exceed
the output limit and kill the turn. Dependencies may reference nodes
you have not added yet; those nodes stay pending until the dependency
exists and is verified. Add missing nodes promptly: a dependency that
never arrives deadlocks the plan.

Per-node worker overrides: `model` (provider/model, or a bare model id
on the current provider), `temperature` (0-1), and `context_window`
(tokens, for local models that report no size). Use them to match the
worker to its job: a cheap fast model for bulk research nodes, the
default model for implementation nodes, low temperature for extraction
and formatting work. Omit a field to inherit the agent's default.

Supervision tree: a node with agent=supervisor does not edit code. It
authors its own sub-plan through the plan tool and runs it, and the
node completes only when that sub-plan completes. Use this for genuinely
large sub-tasks - a whole feature with several independent parts - never
for anything one worker could finish alone. The one rule of the tree:
every deeper level must receive a strictly SIMPLER, smaller task than
the level above it. If a sub-task is not clearly simpler than its
parent, split it into worker nodes yourself instead of delegating the
delegation. Hierarchy is capped at two levels below you.

Actions:
- add: create the plan (or extend it). Provide full nodes: id, title,
  prompt; optionally depends_on, priority, agent (coder|task|supervisor),
  verify_cmd, verify_criteria, files, timeout, max_restarts. The DAG
  must be acyclic. Scheduling starts immediately.
- update: change pending or stalled nodes (prompt, deps, verification,
  priority, files...). Changing a stalled node readopts and un-stalls
  it - that is the adjudication path.
- retry: give stalled or verified nodes a fresh restart budget.
- remove: drop nodes (running workers are killed).
- pause / resume: stop / resume scheduling (running workers are killed
  on pause and requeued on resume).
- set_limit: lower max_parallel and/or set a spend budget in USD.
- status: render the plan, slots, and budgets.

Always end your orchestrated turn with action=status so your reply to
the user reflects real state, and stay silent-ish between wake-ups:
the Supervisor only calls you back when something needs you.
