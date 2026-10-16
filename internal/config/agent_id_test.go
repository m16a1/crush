package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_AgentIDs(t *testing.T) {
	cfg := &Config{
		Options: &Options{
			DisabledTools: []string{},
		},
	}
	cfg.SetupAgents()

	t.Run("Coder agent should have correct ID", func(t *testing.T) {
		coderAgent, ok := cfg.Agents[AgentCoder]
		require.True(t, ok)
		assert.Equal(t, AgentCoder, coderAgent.ID, "Coder agent ID should be '%s'", AgentCoder)
	})

	t.Run("Task agent should have correct ID", func(t *testing.T) {
		taskAgent, ok := cfg.Agents[AgentTask]
		require.True(t, ok)
		assert.Equal(t, AgentTask, taskAgent.ID, "Task agent ID should be '%s'", AgentTask)
	})

	t.Run("Plan agent should have correct ID", func(t *testing.T) {
		planAgent, ok := cfg.Agents[AgentPlan]
		require.True(t, ok)
		assert.Equal(t, AgentPlan, planAgent.ID, "Plan agent ID should be '%s'", AgentPlan)
	})
}

func TestConfig_SetupAgentsOrchestrationPlanTool(t *testing.T) {
	off := &Config{Options: &Options{}}
	off.SetupAgents()
	require.NotContains(t, off.Agents[AgentCoder].AllowedTools, "plan")

	on := &Config{Options: &Options{Orchestration: true}}
	on.SetupAgents()
	require.Contains(t, on.Agents[AgentCoder].AllowedTools, "plan")
	// Read-only agents never orchestrate.
	require.NotContains(t, on.Agents[AgentTask].AllowedTools, "plan")
}
