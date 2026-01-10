//go:build integration

package integration

import (
	"testing"

	"entire.io/cli/cmd/entire/cli/strategy"
)

// strategiesBlockingMain returns strategies that should NOT work on main branch.
// Currently all strategies are allowed on main branch.
func strategiesBlockingMain() []string {
	return []string{}
}

// strategiesAllowingMain returns strategies that SHOULD work on main branch.
// All strategies now support working on main branch.
func strategiesAllowingMain() []string {
	return []string{strategy.StrategyNameManualCommit, strategy.StrategyNameAutoCommit}
}

// TestDefaultBranch_SkipsOnMain_BlockingStrategies tests that auto-commit strategy
// skips on main branch.
func TestDefaultBranch_SkipsOnMain_BlockingStrategies(t *testing.T) {
	t.Parallel()
	for _, strategyName := range strategiesBlockingMain() {
		strategyName := strategyName
		t.Run(strategyName, func(t *testing.T) {
			t.Parallel()
			env := NewRepoWithCommit(t, strategyName)
			defer env.Cleanup()

			branch := env.GetCurrentBranch()
			if branch != "main" && branch != "master" {
				t.Fatalf("expected to be on main or master branch, got %q", branch)
			}

			session := env.NewSession()
			if err := env.SimulateUserPromptSubmit(session.ID); err != nil {
				t.Fatalf("SimulateUserPromptSubmit failed: %v", err)
			}

			env.WriteFile("file.txt", "content on main")
			session.CreateTranscript(
				"Add a file",
				[]FileChange{{Path: "file.txt", Content: "content on main"}},
			)

			if err := env.SimulateStop(session.ID, session.TranscriptPath); err != nil {
				t.Fatalf("SimulateStop failed: %v", err)
			}

			points := env.GetRewindPoints()
			if len(points) != 0 {
				t.Errorf("expected 0 rewind points on main branch for %s strategy, got %d", strategyName, len(points))
			}
		})
	}
}

// TestDefaultBranch_WorksOnMain_ShadowStrategies tests that shadow strategies work on main branch.
func TestDefaultBranch_WorksOnMain_ShadowStrategies(t *testing.T) {
	t.Parallel()
	for _, strategyName := range strategiesAllowingMain() {
		strategyName := strategyName
		t.Run(strategyName, func(t *testing.T) {
			t.Parallel()
			env := NewRepoWithCommit(t, strategyName)
			defer env.Cleanup()

			branch := env.GetCurrentBranch()
			if branch != "main" && branch != "master" {
				t.Fatalf("expected to be on main or master branch, got %q", branch)
			}

			session := env.NewSession()
			if err := env.SimulateUserPromptSubmit(session.ID); err != nil {
				t.Fatalf("SimulateUserPromptSubmit failed: %v", err)
			}

			env.WriteFile("file.txt", "content on main")
			session.CreateTranscript(
				"Add a file",
				[]FileChange{{Path: "file.txt", Content: "content on main"}},
			)

			if err := env.SimulateStop(session.ID, session.TranscriptPath); err != nil {
				t.Fatalf("SimulateStop failed: %v", err)
			}

			points := env.GetRewindPoints()
			if len(points) != 1 {
				t.Errorf("expected 1 rewind point on main branch for %s strategy (shadow strategies allow main), got %d", strategyName, len(points))
			}
		})
	}
}

// TestDefaultBranch_WorksOnFeatureBranch tests that Entire tracking works on feature branches.
func TestDefaultBranch_WorksOnFeatureBranch(t *testing.T) {
	t.Parallel()
	RunForAllStrategies(t, func(t *testing.T, env *TestEnv, strategyName string) {
		branch := env.GetCurrentBranch()
		if branch != "feature/test-branch" {
			t.Fatalf("expected to be on feature/test-branch, got %q", branch)
		}

		session := env.NewSession()
		if err := env.SimulateUserPromptSubmit(session.ID); err != nil {
			t.Fatalf("SimulateUserPromptSubmit failed: %v", err)
		}

		env.WriteFile("feature.txt", "content on feature branch")
		session.CreateTranscript(
			"Add a feature file",
			[]FileChange{{Path: "feature.txt", Content: "content on feature branch"}},
		)

		if err := env.SimulateStop(session.ID, session.TranscriptPath); err != nil {
			t.Fatalf("SimulateStop failed: %v", err)
		}

		points := env.GetRewindPoints()
		if len(points) != 1 {
			t.Errorf("expected 1 rewind point on feature branch, got %d", len(points))
		}
	})
}

// TestDefaultBranch_PostTaskSkipsOnMain_BlockingStrategies tests that task checkpoints are
// skipped on main for commit and auto-commit strategies.
func TestDefaultBranch_PostTaskSkipsOnMain_BlockingStrategies(t *testing.T) {
	t.Parallel()
	for _, strategyName := range strategiesBlockingMain() {
		strategyName := strategyName
		t.Run(strategyName, func(t *testing.T) {
			t.Parallel()
			env := NewRepoWithCommit(t, strategyName)
			defer env.Cleanup()

			branch := env.GetCurrentBranch()
			if branch != "main" && branch != "master" {
				t.Fatalf("expected to be on main or master branch, got %q", branch)
			}

			session := env.NewSession()
			if err := env.SimulateUserPromptSubmit(session.ID); err != nil {
				t.Fatalf("SimulateUserPromptSubmit failed: %v", err)
			}

			session.TranscriptBuilder.AddUserMessage("Create a file using a subagent")
			session.TranscriptBuilder.AddAssistantMessage("I'll use the Task tool.")

			taskID := "toolu_task_main"
			agentID := "agent_main_xyz"

			session.TranscriptBuilder.AddTaskToolUse(taskID, "Create task.txt")
			if err := session.TranscriptBuilder.WriteToFile(session.TranscriptPath); err != nil {
				t.Fatalf("failed to write transcript: %v", err)
			}

			if err := env.SimulatePreTask(session.ID, session.TranscriptPath, taskID); err != nil {
				t.Fatalf("SimulatePreTask failed: %v", err)
			}

			env.WriteFile("task.txt", "Created by task on main")

			session.TranscriptBuilder.AddTaskToolResult(taskID, agentID)
			if err := session.TranscriptBuilder.WriteToFile(session.TranscriptPath); err != nil {
				t.Fatalf("failed to write transcript: %v", err)
			}

			if err := env.SimulatePostTask(PostTaskInput{
				SessionID:      session.ID,
				TranscriptPath: session.TranscriptPath,
				ToolUseID:      taskID,
				AgentID:        agentID,
			}); err != nil {
				t.Fatalf("SimulatePostTask failed: %v", err)
			}

			points := env.GetRewindPoints()
			if len(points) != 0 {
				t.Errorf("expected 0 rewind points (task checkpoints skipped on main for %s), got %d", strategyName, len(points))
			}
		})
	}
}

// TestDefaultBranch_PostTaskWorksOnMain_ShadowStrategies tests that task checkpoints work
// on main for shadow strategies.
func TestDefaultBranch_PostTaskWorksOnMain_ShadowStrategies(t *testing.T) {
	t.Parallel()
	for _, strategyName := range strategiesAllowingMain() {
		strategyName := strategyName
		t.Run(strategyName, func(t *testing.T) {
			t.Parallel()
			env := NewRepoWithCommit(t, strategyName)
			defer env.Cleanup()

			branch := env.GetCurrentBranch()
			if branch != "main" && branch != "master" {
				t.Fatalf("expected to be on main or master branch, got %q", branch)
			}

			session := env.NewSession()
			if err := env.SimulateUserPromptSubmit(session.ID); err != nil {
				t.Fatalf("SimulateUserPromptSubmit failed: %v", err)
			}

			session.TranscriptBuilder.AddUserMessage("Create a file using a subagent")
			session.TranscriptBuilder.AddAssistantMessage("I'll use the Task tool.")

			taskID := "toolu_task_main"
			agentID := "agent_main_xyz"

			session.TranscriptBuilder.AddTaskToolUse(taskID, "Create task.txt")
			if err := session.TranscriptBuilder.WriteToFile(session.TranscriptPath); err != nil {
				t.Fatalf("failed to write transcript: %v", err)
			}

			if err := env.SimulatePreTask(session.ID, session.TranscriptPath, taskID); err != nil {
				t.Fatalf("SimulatePreTask failed: %v", err)
			}

			env.WriteFile("task.txt", "Created by task on main")

			session.TranscriptBuilder.AddTaskToolResult(taskID, agentID)
			if err := session.TranscriptBuilder.WriteToFile(session.TranscriptPath); err != nil {
				t.Fatalf("failed to write transcript: %v", err)
			}

			if err := env.SimulatePostTask(PostTaskInput{
				SessionID:      session.ID,
				TranscriptPath: session.TranscriptPath,
				ToolUseID:      taskID,
				AgentID:        agentID,
			}); err != nil {
				t.Fatalf("SimulatePostTask failed: %v", err)
			}

			points := env.GetRewindPoints()
			if len(points) != 2 {
				t.Errorf("expected 2 rewind points (starting + completed checkpoints work on main for %s), got %d", strategyName, len(points))
			}
		})
	}
}
