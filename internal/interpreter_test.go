package internal

import (
	"strings"
	"testing"
)

// TestInterpretOutput tests the svelte-check --output machine-verbose interpreter.
// The interpreter parses streaming output and emits typed events.

func TestInterpretOutput_EmitsStartEvent(t *testing.T) {
	input := `1770255832071 START "/Users/tyler/src/myproject"
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	event := <-events
	start, ok := event.(SvelteWatchCheckStart)
	if !ok {
		t.Fatalf("Expected SvelteWatchCheckStart, got %T", event)
	}
	if start.Timestamp != 1770255832071 {
		t.Errorf("Timestamp = %d, want 1770255832071", start.Timestamp)
	}
	if start.Workspace != "/Users/tyler/src/myproject" {
		t.Errorf("Workspace = %q, want %q", start.Workspace, "/Users/tyler/src/myproject")
	}
}

func TestInterpretOutput_EmitsCompleteEvent(t *testing.T) {
	input := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/lib/utils.ts","start":{"line":0,"character":38},"end":{"line":0,"character":44},"message":"Cannot find module 'clsx'","code":2307}
1770255834342 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	// First event: Start
	event := <-events
	if _, ok := event.(SvelteWatchCheckStart); !ok {
		t.Fatalf("Expected SvelteWatchCheckStart, got %T", event)
	}

	// Second event: Complete
	event = <-events
	completed, ok := event.(SvelteWatchCheckComplete)
	if !ok {
		t.Fatalf("Expected SvelteWatchCheckComplete, got %T", event)
	}

	if completed.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", completed.ErrorCount)
	}
	if completed.WarningCount != 0 {
		t.Errorf("WarningCount = %d, want 0", completed.WarningCount)
	}
	if completed.FileCount != 100 {
		t.Errorf("FileCount = %d, want 100", completed.FileCount)
	}
	if len(completed.Diagnostics) != 1 {
		t.Fatalf("Diagnostics count = %d, want 1", len(completed.Diagnostics))
	}

	diag := completed.Diagnostics[0]
	if diag.Filename != "src/lib/utils.ts" {
		t.Errorf("Diagnostic filename = %q, want %q", diag.Filename, "src/lib/utils.ts")
	}
	if diag.Type != "ERROR" {
		t.Errorf("Diagnostic type = %q, want %q", diag.Type, "ERROR")
	}
	if !strings.Contains(diag.Message, "Cannot find module 'clsx'") {
		t.Errorf("Diagnostic message = %q, want to contain %q", diag.Message, "Cannot find module 'clsx'")
	}
}

func TestInterpretOutput_CountsErrorsAndWarnings(t *testing.T) {
	input := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"Error one","code":2322}
1770255834342 {"type":"ERROR","filename":"src/b.ts","start":{"line":1,"character":0},"end":{"line":1,"character":1},"message":"Error two","code":2322}
1770255834342 {"type":"WARNING","filename":"src/c.svelte","start":{"line":2,"character":0},"end":{"line":2,"character":1},"message":"Warning one","code":"a11y_missing_attribute","source":"svelte"}
1770255834342 {"type":"WARNING","filename":"src/d.svelte","start":{"line":3,"character":0},"end":{"line":3,"character":1},"message":"Warning two","code":"css_unused_selector","source":"svelte"}
1770255834342 {"type":"WARNING","filename":"src/e.svelte","start":{"line":4,"character":0},"end":{"line":4,"character":1},"message":"Warning three","code":"export_let_unused","source":"svelte"}
1770255834342 COMPLETED 100 FILES 2 ERRORS 3 WARNINGS 5 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	<-events // Start
	event := <-events
	completed := event.(SvelteWatchCheckComplete)

	if completed.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", completed.ErrorCount)
	}
	if completed.WarningCount != 3 {
		t.Errorf("WarningCount = %d, want 3", completed.WarningCount)
	}
	if len(completed.Diagnostics) != 5 {
		t.Errorf("Diagnostics count = %d, want 5", len(completed.Diagnostics))
	}
}

func TestInterpretOutput_MultipleCycles(t *testing.T) {
	// Simulate two check cycles (file change triggers recheck)
	input := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"First error","code":2322}
1770255834342 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
1770255844663 START "/workspace"
1770255844689 {"type":"ERROR","filename":"src/b.ts","start":{"line":1,"character":0},"end":{"line":1,"character":1},"message":"Second error","code":2322}
1770255844689 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	// First cycle
	<-events // Start
	event1 := <-events
	completed1 := event1.(SvelteWatchCheckComplete)
	if len(completed1.Diagnostics) != 1 {
		t.Fatalf("First cycle should have 1 diagnostic, got %d", len(completed1.Diagnostics))
	}
	if completed1.Diagnostics[0].Message != "First error" {
		t.Errorf("First cycle should have 'First error', got: %q", completed1.Diagnostics[0].Message)
	}

	// Second cycle
	<-events // Start
	event2 := <-events
	completed2 := event2.(SvelteWatchCheckComplete)
	if len(completed2.Diagnostics) != 1 {
		t.Fatalf("Second cycle should have 1 diagnostic, got %d", len(completed2.Diagnostics))
	}
	if completed2.Diagnostics[0].Message != "Second error" {
		t.Errorf("Second cycle should have 'Second error', got: %q", completed2.Diagnostics[0].Message)
	}
}

func TestInterpretOutput_ZeroErrorsAndWarnings(t *testing.T) {
	input := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	<-events // Start
	event := <-events
	completed := event.(SvelteWatchCheckComplete)

	if completed.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0", completed.ErrorCount)
	}
	if completed.WarningCount != 0 {
		t.Errorf("WarningCount = %d, want 0", completed.WarningCount)
	}
	if len(completed.Diagnostics) != 0 {
		t.Errorf("Diagnostics should be empty for clean check, got %d", len(completed.Diagnostics))
	}
}

func TestInterpretOutput_ParsesDiagnosticTimestamp(t *testing.T) {
	input := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"Error","code":2322}
1770255834342 COMPLETED 100 FILES 1 ERRORS 0 WARNINGS 1 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	<-events // Start
	event := <-events
	completed := event.(SvelteWatchCheckComplete)

	// Check that timestamp was added to diagnostic
	if completed.Diagnostics[0].Timestamp != 1770255834342 {
		t.Errorf("Diagnostic timestamp = %d, want 1770255834342", completed.Diagnostics[0].Timestamp)
	}
}

func TestInterpretOutput_ParsesNumericAndStringCodes(t *testing.T) {
	input := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"TS Error","code":2322}
1770255834342 {"type":"WARNING","filename":"src/b.svelte","start":{"line":1,"character":0},"end":{"line":1,"character":1},"message":"Svelte Warning","code":"a11y_missing_attribute","source":"svelte"}
1770255834342 COMPLETED 100 FILES 1 ERRORS 1 WARNINGS 2 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	<-events // Start
	event := <-events
	completed := event.(SvelteWatchCheckComplete)

	// TypeScript error has numeric code
	tsCode, ok := completed.Diagnostics[0].Code.(float64) // JSON unmarshals numbers as float64
	if !ok {
		t.Errorf("TS error code should be numeric, got %T", completed.Diagnostics[0].Code)
	}
	if tsCode != 2322 {
		t.Errorf("TS error code = %v, want 2322", tsCode)
	}

	// Svelte warning has string code
	svelteCode, ok := completed.Diagnostics[1].Code.(string)
	if !ok {
		t.Errorf("Svelte warning code should be string, got %T", completed.Diagnostics[1].Code)
	}
	if svelteCode != "a11y_missing_attribute" {
		t.Errorf("Svelte warning code = %q, want %q", svelteCode, "a11y_missing_attribute")
	}
}

func TestInterpretOutput_EmitsFailureEvent(t *testing.T) {
	input := `1770255832071 START "/workspace"
1770255834342 FAILURE "Connection closed"
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	<-events // Start
	event := <-events
	failure, ok := event.(SvelteWatchFailure)
	if !ok {
		t.Fatalf("Expected SvelteWatchFailure, got %T", event)
	}

	if failure.Timestamp != 1770255834342 {
		t.Errorf("Timestamp = %d, want 1770255834342", failure.Timestamp)
	}
	if failure.Message != "Connection closed" {
		t.Errorf("Message = %q, want %q", failure.Message, "Connection closed")
	}
}

func TestInterpretOutput_SkipsCommentsAndEmptyLines(t *testing.T) {
	input := `# This is a comment
1770255832071 START "/workspace"

# Another comment
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	events := make(chan SvelteCheckEvent, 10)

	go func() {
		if err := InterpretOutput(strings.NewReader(input), events); err != nil {
			t.Errorf("InterpretOutput error: %v", err)
		}
		close(events)
	}()

	// Should still get Start and Complete events
	event := <-events
	if _, ok := event.(SvelteWatchCheckStart); !ok {
		t.Fatalf("Expected SvelteWatchCheckStart, got %T", event)
	}

	event = <-events
	if _, ok := event.(SvelteWatchCheckComplete); !ok {
		t.Fatalf("Expected SvelteWatchCheckComplete, got %T", event)
	}
}

func TestFormatHuman_NoIssues(t *testing.T) {
	event := SvelteWatchCheckComplete{
		FileCount:    100,
		ErrorCount:   0,
		WarningCount: 0,
		Diagnostics:  nil,
	}

	output := FormatHuman(event)
	if !strings.Contains(output, "no issues") {
		t.Errorf("Output should indicate no issues, got: %q", output)
	}
	if !strings.Contains(output, "100 files") {
		t.Errorf("Output should mention file count, got: %q", output)
	}
}

func TestFormatHuman_WithDiagnostics(t *testing.T) {
	event := SvelteWatchCheckComplete{
		FileCount:    100,
		ErrorCount:   1,
		WarningCount: 1,
		Diagnostics: []Diagnostic{
			{
				Type:     "ERROR",
				Filename: "src/lib/utils.ts",
				Start:    Position{Line: 0, Character: 10},
				Message:  "Type 'string' is not assignable to type 'number'.",
			},
			{
				Type:     "WARNING",
				Filename: "src/components/Button.svelte",
				Start:    Position{Line: 5, Character: 0},
				Message:  "Unused CSS selector",
			},
		},
	}

	output := FormatHuman(event)

	// Should show filename:line:char format (1-based)
	if !strings.Contains(output, "src/lib/utils.ts:1:11") {
		t.Errorf("Output should have 1-based line:char, got: %q", output)
	}
	if !strings.Contains(output, "ERROR") {
		t.Errorf("Output should show ERROR, got: %q", output)
	}
	if !strings.Contains(output, "WARNING") {
		t.Errorf("Output should show WARNING, got: %q", output)
	}
	if !strings.Contains(output, "1 errors, 1 warnings") {
		t.Errorf("Output should have summary, got: %q", output)
	}
}
