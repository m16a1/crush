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

Actions:
- add: create the plan (or extend it). Provide full nodes: id, title,
  prompt; optionally depends_on, priority, agent (coder|task),
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
