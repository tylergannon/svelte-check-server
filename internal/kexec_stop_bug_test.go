package internal

import (
	"context"
	"testing"
	"time"

	kexec "k8s.io/utils/exec"
)

// TestKexecStop_WaitPreventsAfterFuncPanic verifies that calling Wait() after Start()
// prevents a panic in kexec's Stop() method.
//
// Without Wait(), kexec's Stop() schedules a 10-second timer that checks
// ProcessState.Exited(). If the process exits but Wait() was never called,
// ProcessState is nil and the timer panics.
//
// Our fix: Runner.Start() spawns a goroutine that calls Wait().
func TestKexecStop_WaitPreventsAfterFuncPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping 11-second test in short mode")
	}

	executor := kexec.New()

	ctx := context.Background()
	cmd := executor.CommandContext(ctx, "sleep", "60")

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Call Wait() in a goroutine - this is what Runner.Start() does
	go func() {
		_ = cmd.Wait()
	}()

	cmd.Stop()

	// Wait 11 seconds - without the Wait() goroutine, this would panic
	time.Sleep(11 * time.Second)
}
