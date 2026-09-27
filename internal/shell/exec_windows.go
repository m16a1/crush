//go:build windows

package shell

import (
	"os/exec"
	"time"

	"mvdan.cc/sh/v3/interp"
)

// defaultKillTimeout matches mvdan's DefaultExecHandler default.
const defaultKillTimeout = 2 * time.Second

// isolateProcess is a no-op on Windows. Session isolation via Setsid is a
// Unix-only concept; Windows uses CREATE_NEW_PROCESS_GROUP which mvdan's
// default handler already handles adequately.
func isolateProcess(_ *exec.Cmd) {}

// processGroupExecHandler returns interp.DefaultExecHandler on Windows.
// The onProcessStart callback is unused here because the default handler
// does not expose the spawned process; PID reporting is Unix-only.
func processGroupExecHandler(killTimeout time.Duration, _ func(int)) interp.ExecHandlerFunc {
	return interp.DefaultExecHandler(killTimeout)
}
