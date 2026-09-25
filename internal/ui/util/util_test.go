package util

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestMessageConstructorsSetTheRightType(t *testing.T) {
	t.Parallel()

	require.Equal(t, InfoMsg{Type: InfoTypeInfo, Msg: "hi"}, NewInfoMsg("hi"))
	require.Equal(t, InfoMsg{Type: InfoTypeWarn, Msg: "careful"}, NewWarnMsg("careful"))
	require.Equal(t, InfoMsg{Type: InfoTypeError, Msg: "boom"}, NewErrorMsg(errors.New("boom")))
}

func TestInfoMsgIsEmpty(t *testing.T) {
	t.Parallel()

	require.True(t, InfoMsg{}.IsEmpty())
	require.False(t, InfoMsg{Msg: "x"}.IsEmpty())
	require.False(t, InfoMsg{TTL: time.Second}.IsEmpty())
	require.False(t, InfoMsg{Type: InfoTypeWarn}.IsEmpty())
}

func TestReportCommandsEmitTheirMessage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cmd  tea.Cmd
		want InfoMsg
	}{
		"info":  {ReportInfo("done"), NewInfoMsg("done")},
		"warn":  {ReportWarn("careful"), NewWarnMsg("careful")},
		"error": {ReportError(errors.New("boom")), NewErrorMsg(errors.New("boom"))},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.cmd())
		})
	}
}

func TestCmdHandlerReturnsTheSameMessage(t *testing.T) {
	t.Parallel()

	msg := InfoMsg{Type: InfoTypeSuccess, Msg: "ok"}
	require.Equal(t, msg, CmdHandler(msg)())
	require.Equal(t, ClearStatusMsg{}, CmdHandler(ClearStatusMsg{})())
}

func TestExecShellRejectsCommandsThatDoNotParse(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":      "",
		"blank":      "   ",
		"unbalanced": `"unterminated`,
	}

	for name, cmdStr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			msg, ok := ExecShell(t.Context(), cmdStr, nil)().(InfoMsg)
			require.True(t, ok, "a parse failure must surface an InfoMsg error, not run a command")
			require.Equal(t, InfoTypeError, msg.Type)
		})
	}
}

func TestExecShellProducesACommandForValidInput(t *testing.T) {
	t.Parallel()

	require.NotNil(t, ExecShell(t.Context(), `echo "hello world"`, nil))
	require.NotNil(t, ExecShell(t.Context(), "true", nil))
}
