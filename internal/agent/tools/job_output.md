Get stdout/stderr from a background shell by ID.

<usage>
- Set wait=true to block until the shell finishes. Do not run a separate `sleep` command and poll; waiting in one call is what this is for.
- Combine wait=true with timeout_ms to block for at most that long. The current output is returned even if the shell is still running, which is how you wait for a server or watcher to become ready.
- Without wait, the call returns immediately with the output produced so far and whether the shell has finished.
</usage>
