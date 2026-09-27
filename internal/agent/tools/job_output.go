package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/shell"
)

const (
	JobOutputToolName = "job_output"
)

//go:embed job_output.md
var jobOutputDescription string

type JobOutputParams struct {
	ShellID   string `json:"shell_id" description:"The ID of the background shell to retrieve output from"`
	Wait      bool   `json:"wait" description:"If true, block until the background shell completes before returning output"`
	TimeoutMs int    `json:"timeout_ms,omitempty" description:"When wait is true, return the current output after this many milliseconds even if the shell is still running. Use this to wait for a long-running command to reach a ready state without spinning."`
}

type JobOutputResponseMetadata struct {
	ShellID          string `json:"shell_id"`
	Command          string `json:"command"`
	Description      string `json:"description"`
	Done             bool   `json:"done"`
	ExitCode         int    `json:"exit_code,omitempty"`
	PID              int    `json:"pid,omitempty"`
	WorkingDirectory string `json:"working_directory"`
}

func NewJobOutputTool(spillDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		JobOutputToolName,
		jobOutputDescription,
		func(ctx context.Context, params JobOutputParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.ShellID == "" {
				return fantasy.NewTextErrorResponse("missing shell_id"), nil
			}

			bgManager := shell.GetBackgroundShellManager()
			bgShell, ok := bgManager.Get(params.ShellID)
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("background shell not found: %s", params.ShellID)), nil
			}

			if params.Wait {
				if params.TimeoutMs > 0 {
					// Bounded wait: block until the shell finishes or the
					// timeout elapses, whichever comes first. This is the
					// supported alternative to sleeping and polling, and it
					// works for long-running commands that never exit.
					waitCtx, cancel := context.WithTimeout(ctx, time.Duration(params.TimeoutMs)*time.Millisecond)
					defer cancel()
					bgShell.WaitContext(waitCtx)
				} else {
					bgShell.WaitContext(ctx)
				}
			}

			stdout, stderr, done, err := bgShell.GetOutput()

			var outputParts []string
			if stdout != "" {
				outputParts = append(outputParts, stdout)
			}
			if stderr != "" {
				outputParts = append(outputParts, stderr)
			}

			status := "running"
			if done {
				status = "completed"
				if err != nil {
					exitCode := shell.ExitCode(err)
					if exitCode != 0 {
						outputParts = append(outputParts, fmt.Sprintf("Exit code %d", exitCode))
					}
				}
			}

			output := strings.Join(outputParts, "\n")
			output = TruncateOutput(output, spillDir)

			metadata := JobOutputResponseMetadata{
				ShellID:          params.ShellID,
				Command:          bgShell.Command,
				Description:      bgShell.Description,
				Done:             done,
				ExitCode:         shell.ExitCode(err),
				PID:              bgShell.PID(),
				WorkingDirectory: bgShell.WorkingDir,
			}

			if output == "" {
				output = BashNoOutput
			}

			result := fmt.Sprintf("Status: %s\n\n%s", status, output)
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(result), metadata), nil
		},
	)
}
