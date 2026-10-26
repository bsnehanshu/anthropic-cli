package cmd

import (
	"testing"

	"github.com/anthropics/anthropic-cli/v2/internal/mocktest"
)

func TestOrganizationWorkspacesMembersRetrieve(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:workspaces:members", "retrieve",
			"--workspace-id", "workspace_id",
			"--user-id", "user_id",
		)
	})
}

func TestOrganizationWorkspacesMembersUpdate(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:workspaces:members", "update",
			"--workspace-id", "workspace_id",
			"--user-id", "user_id",
			"--workspace-role", "workspace_admin",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("workspace_role: workspace_admin")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"organization:workspaces:members", "update",
			"--workspace-id", "workspace_id",
			"--user-id", "user_id",
		)
	})
}

func TestOrganizationWorkspacesMembersList(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:workspaces:members", "list",
			"--max-items", "10",
			"--workspace-id", "workspace_id",
			"--after-id", "after_id",
			"--before-id", "before_id",
			"--limit", "1",
		)
	})
}

func TestOrganizationWorkspacesMembersAdd(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:workspaces:members", "add",
			"--workspace-id", "workspace_id",
			"--user-id", "user_01WxYzAbCdEfGhJkMnPqRsTu",
			"--workspace-role", "workspace_admin",
		)
	})

	t.Run("piping data", func(t *testing.T) {
		// Test piping YAML data over stdin
		pipeData := []byte("" +
			"user_id: user_01WxYzAbCdEfGhJkMnPqRsTu\n" +
			"workspace_role: workspace_admin\n")
		mocktest.TestRunMockTestWithPipeAndFlags(
			t, pipeData,
			"organization:workspaces:members", "add",
			"--workspace-id", "workspace_id",
		)
	})
}

func TestOrganizationWorkspacesMembersRemove(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "string")
	t.Run("regular flags", func(t *testing.T) {
		mocktest.TestRunMockTestWithFlags(
			t,
			"organization:workspaces:members", "remove",
			"--workspace-id", "workspace_id",
			"--user-id", "user_id",
		)
	})
}
