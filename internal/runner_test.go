package internal

import (
	"bytes"
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"

	kexec "k8s.io/utils/exec"
)

// FakeCmd implements kexec.Cmd for testing.
type FakeCmd struct {
	dir        string
	stdout     io.ReadCloser
	stderr     io.ReadCloser
	started    bool
	stopped    bool
	startError error
}

func (c *FakeCmd) SetDir(dir string)                                    { c.dir = dir }
func (c *FakeCmd) SetStdin(in io.Reader)                                {}
func (c *FakeCmd) SetStdout(out io.Writer)                              {}
func (c *FakeCmd) SetStderr(out io.Writer)                              {}
func (c *FakeCmd) SetEnv(env []string)                                  {}
func (c *FakeCmd) StdoutPipe() (io.ReadCloser, error)                   { return c.stdout, nil }
func (c *FakeCmd) StderrPipe() (io.ReadCloser, error)                   { return c.stderr, nil }
func (c *FakeCmd) Start() error                                         { c.started = true; return c.startError }
func (c *FakeCmd) Wait() error                                          { return nil }
func (c *FakeCmd) Run() error                                           { return nil }
func (c *FakeCmd) CombinedOutput() ([]byte, error)                      { return nil, nil }
func (c *FakeCmd) Output() ([]byte, error)                              { return nil, nil }
func (c *FakeCmd) Stop()                                                { c.stopped = true }
func (c *FakeCmd) SetProcessGroupCreation(_ bool)                       {}
func (c *FakeCmd) SetProcessGroupPgid(_ bool)                           {}
func (c *FakeCmd) SetProcessGroupPdeathsig(_ bool)                      {}
func (c *FakeCmd) GetProcessGroupProcess() (*int, error)                { return nil, nil }
func (c *FakeCmd) SetTerminateGracePeriod(_ time.Duration)              {}
func (c *FakeCmd) SetTerminateGracePeriodWithContext(_ context.Context) {}
func (c *FakeCmd) SetTerminateGracePeriodWithTimer(_ *time.Timer)       {}
func (c *FakeCmd) SetTerminateGracePeriodWithoutKilling()               {}

// FakeExecutor implements kexec.Interface for testing.
type FakeExecutor struct {
	cmd *FakeCmd
}

func NewFakeExecutor(stdout, stderr string) *FakeExecutor {
	return &FakeExecutor{
		cmd: &FakeCmd{
			stdout: io.NopCloser(bytes.NewBufferString(stdout)),
			stderr: io.NopCloser(bytes.NewBufferString(stderr)),
		},
	}
}

func (e *FakeExecutor) Command(cmd string, args ...string) kexec.Cmd {
	return e.cmd
}

func (e *FakeExecutor) CommandContext(ctx context.Context, cmd string, args ...string) kexec.Cmd {
	return e.cmd
}

func (e *FakeExecutor) LookPath(file string) (string, error) {
	return file, nil
}

// TestNewRunner tests the NewRunner constructor.
func TestNewRunner(t *testing.T) {
	executor := NewFakeExecutor("", "")
	r := NewRunner("/workspace", "/workspace/tsconfig.json", executor)

	if r.workspacePath != "/workspace" {
		t.Errorf("workspacePath = %q, want /workspace", r.workspacePath)
	}
	if r.tsconfigPath != "/workspace/tsconfig.json" {
		t.Errorf("tsconfigPath = %q, want /workspace/tsconfig.json", r.tsconfigPath)
	}
	if r.latest == nil {
		t.Error("latest channel not initialized")
	}
}

// TestRunner_Start tests starting the runner.
func TestRunner_Start(t *testing.T) {
	// Simulate svelte-check output
	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	err := r.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !executor.cmd.started {
		t.Error("Command was not started")
	}
	if executor.cmd.dir != "/workspace" {
		t.Errorf("Command dir = %q, want /workspace", executor.cmd.dir)
	}
}

// TestRunner_Stop tests stopping the runner.
func TestRunner_Stop(t *testing.T) {
	executor := NewFakeExecutor("", "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	r.Stop()

	if !executor.cmd.stopped {
		t.Error("Command was not stopped")
	}
}

// TestRunner_Stop_NilCmd tests stopping when cmd is nil.
func TestRunner_Stop_NilCmd(t *testing.T) {
	executor := NewFakeExecutor("", "")
	r := NewRunner("/workspace", "", executor)

	// Should not panic when cmd is nil
	r.Stop()
}

// TestRunner_GetLatestEvent_BlocksUntilComplete tests that GetLatestEvent blocks.
func TestRunner_GetLatestEvent_BlocksUntilComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Output with machine-verbose format
		output := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"Test error","code":2322}
1770255834342 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
`
		executor := NewFakeExecutor(output, "")
		r := NewRunner("/workspace", "", executor)

		ctx := context.Background()
		_ = r.Start(ctx)

		// Give the interpreter time to process
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		result := r.GetLatestEvent()

		if result.ErrorCount != 1 {
			t.Errorf("ErrorCount = %d, want 1", result.ErrorCount)
		}
	})
}

// TestRunner_GetLatestEvent_ReturnsValueMultipleTimes tests read-then-write-back.
func TestRunner_GetLatestEvent_ReturnsValueMultipleTimes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
		executor := NewFakeExecutor(output, "")
		r := NewRunner("/workspace", "", executor)

		ctx := context.Background()
		_ = r.Start(ctx)

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		// Call GetLatestEvent multiple times - should return same value each time
		result1 := r.GetLatestEvent()
		result2 := r.GetLatestEvent()
		result3 := r.GetLatestEvent()

		// Since structs with slices can't be compared with ==, compare key fields
		if result1.ErrorCount != result2.ErrorCount || result2.ErrorCount != result3.ErrorCount {
			t.Error("GetLatestEvent should return same value on multiple calls")
		}
		if result1.FileCount != result2.FileCount || result2.FileCount != result3.FileCount {
			t.Error("GetLatestEvent should return same value on multiple calls")
		}
	})
}

// TestRunner_HandleEvents_StartDrainsChannel tests that start event drains the channel.
func TestRunner_HandleEvents_StartDrainsChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// First complete, then new start (simulating a file change trigger)
		output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
1770255844663 START "/workspace"
1770255844689 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"New error","code":2322}
1770255844689 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
`
		executor := NewFakeExecutor(output, "")
		r := NewRunner("/workspace", "", executor)

		ctx := context.Background()
		_ = r.Start(ctx)

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		// Should get the second (latest) result
		result := r.GetLatestEvent()

		if result.ErrorCount != 1 {
			t.Errorf("ErrorCount = %d, want 1 (should be latest result)", result.ErrorCount)
		}
	})
}

// TestRunner_Restart tests restarting the runner.
func TestRunner_Restart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
		executor := NewFakeExecutor(output, "")
		r := NewRunner("/workspace", "", executor)

		ctx := context.Background()
		_ = r.Start(ctx)

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		// Pre-fill channel with a value
		_ = r.GetLatestEvent()

		// Reset the executor for the restart
		executor.cmd = &FakeCmd{
			stdout: io.NopCloser(bytes.NewBufferString(output)),
			stderr: io.NopCloser(bytes.NewBufferString("")),
		}

		err := r.Restart(ctx)
		if err != nil {
			t.Fatalf("Restart failed: %v", err)
		}

		// Give time for restart
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()

		// Should be able to get a result after restart
		result := r.GetLatestEvent()
		if result.ErrorCount != 0 {
			t.Errorf("ErrorCount = %d after restart, want 0", result.ErrorCount)
		}
	})
}

// TestRunner_HandleEvents_CompleteDrainsOldValue tests that new complete replaces old.
func TestRunner_HandleEvents_CompleteDrainsOldValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Two complete cycles
		output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
1770255844663 START "/workspace"
1770255844689 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"New error","code":2322}
1770255844689 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
`
		executor := NewFakeExecutor(output, "")
		r := NewRunner("/workspace", "", executor)

		ctx := context.Background()
		_ = r.Start(ctx)

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		// Should have the latest result (1 error), not the first (0 errors)
		result := r.GetLatestEvent()

		if result.ErrorCount != 1 {
			t.Errorf("ErrorCount = %d, want 1 (latest result)", result.ErrorCount)
		}
	})
}
