package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// =============================================================================
// Diagnostic Types
// =============================================================================

// Position represents a location in a file.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Diagnostic represents a single error or warning from svelte-check.
// The Timestamp field is extracted from the machine-verbose output prefix
// and added to the struct for clean JSONL output.
type Diagnostic struct {
	Timestamp int64    `json:"timestamp"`
	Type      string   `json:"type"` // "ERROR" or "WARNING"
	Filename  string   `json:"filename"`
	Start     Position `json:"start"`
	End       Position `json:"end"`
	Message   string   `json:"message"`
	Code      any      `json:"code"`             // int for TS errors, string for Svelte warnings
	Source    string   `json:"source,omitempty"` // "js", "ts", "svelte", "css", or empty
}

// =============================================================================
// Events
// =============================================================================

// SvelteCheckEvent represents an event from the svelte-check output stream.
type SvelteCheckEvent interface {
	implementsSvelteCheckEvent()
}

// SvelteWatchCheckStart is emitted when svelte-check begins a new check cycle.
type SvelteWatchCheckStart struct {
	Timestamp int64  `json:"timestamp"`
	Workspace string `json:"workspace"`
}

func (SvelteWatchCheckStart) implementsSvelteCheckEvent() {}

// SvelteWatchCheckComplete is emitted when svelte-check finishes a check cycle.
type SvelteWatchCheckComplete struct {
	Timestamp         int64        `json:"timestamp"`
	Diagnostics       []Diagnostic `json:"diagnostics"`
	FileCount         int          `json:"fileCount"`
	ErrorCount        int          `json:"errorCount"`
	WarningCount      int          `json:"warningCount"`
	FilesWithProblems int          `json:"filesWithProblems"`
}

func (SvelteWatchCheckComplete) implementsSvelteCheckEvent() {}

// SvelteWatchFailure is emitted when svelte-check encounters a runtime error.
type SvelteWatchFailure struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

func (SvelteWatchFailure) implementsSvelteCheckEvent() {}

// =============================================================================
// Interpreter
// =============================================================================

// InterpretOutput reads svelte-check --output machine-verbose output and sends events to the channel.
// It blocks until the reader is closed or returns an error.
// The channel is NOT closed when the function returns - caller owns the channel.
func InterpretOutput(r io.Reader, events chan<- SvelteCheckEvent) error {
	scanner := bufio.NewScanner(r)
	var diagnostics []Diagnostic

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments (for test fixtures)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse timestamp prefix: "1770310077701 ..."
		timestamp, rest, ok := parseTimestampPrefix(line)
		if !ok {
			continue
		}

		// Check for START event: 1770310077701 START "/workspace/path"
		if after, ok0 := strings.CutPrefix(rest, "START "); ok0 {
			workspace := strings.Trim(after, `"`)
			diagnostics = nil // Reset for new cycle
			events <- SvelteWatchCheckStart{
				Timestamp: timestamp,
				Workspace: workspace,
			}
			continue
		}

		// Check for COMPLETED event: 1770310077701 COMPLETED 159 FILES 9 ERRORS 7 WARNINGS 4 FILES_WITH_PROBLEMS
		if strings.HasPrefix(rest, "COMPLETED ") {
			fileCount, errorCount, warningCount, filesWithProblems := parseCompletedLine(rest)
			events <- SvelteWatchCheckComplete{
				Timestamp:         timestamp,
				Diagnostics:       diagnostics,
				FileCount:         fileCount,
				ErrorCount:        errorCount,
				WarningCount:      warningCount,
				FilesWithProblems: filesWithProblems,
			}
			diagnostics = nil // Reset for next cycle
			continue
		}

		// Check for FAILURE event: 1770310077701 FAILURE "Connection closed"
		if after, ok0 := strings.CutPrefix(rest, "FAILURE "); ok0 {
			message := strings.Trim(after, `"`)
			events <- SvelteWatchFailure{
				Timestamp: timestamp,
				Message:   message,
			}
			continue
		}

		// Try to parse as JSON diagnostic
		if strings.HasPrefix(rest, "{") {
			var diag Diagnostic
			if err := json.Unmarshal([]byte(rest), &diag); err == nil {
				diag.Timestamp = timestamp
				diagnostics = append(diagnostics, diag)
			}
		}
	}

	return scanner.Err()
}

// parseTimestampPrefix extracts the timestamp and remaining content from a line.
// Returns (timestamp, rest, ok).
func parseTimestampPrefix(line string) (int64, string, bool) {
	before, after, ok := strings.Cut(line, " ")
	if !ok {
		return 0, "", false
	}

	timestamp, err := strconv.ParseInt(before, 10, 64)
	if err != nil {
		return 0, "", false
	}

	return timestamp, after, true
}

// parseCompletedLine parses a COMPLETED line and extracts counts.
// Format: "COMPLETED 159 FILES 9 ERRORS 7 WARNINGS 4 FILES_WITH_PROBLEMS"
func parseCompletedLine(rest string) (fileCount, errorCount, warningCount, filesWithProblems int) {
	parts := strings.Fields(rest)
	// parts: ["COMPLETED", "159", "FILES", "9", "ERRORS", "7", "WARNINGS", "4", "FILES_WITH_PROBLEMS"]
	if len(parts) >= 9 {
		fileCount, _ = strconv.Atoi(parts[1])
		errorCount, _ = strconv.Atoi(parts[3])
		warningCount, _ = strconv.Atoi(parts[5])
		filesWithProblems, _ = strconv.Atoi(parts[7])
	}
	return
}

// =============================================================================
// Output Formatting
// =============================================================================

// FormatHuman formats a SvelteWatchCheckComplete as human-readable output.
func FormatHuman(event SvelteWatchCheckComplete) string {
	if len(event.Diagnostics) == 0 {
		return fmt.Sprintf("svelte-check found no issues (%d files checked)\n", event.FileCount)
	}

	var sb strings.Builder

	for _, d := range event.Diagnostics {
		// Format: filename:line:char - TYPE: message
		typeStr := d.Type
		sb.WriteString(fmt.Sprintf("%s:%d:%d - %s: %s\n",
			d.Filename,
			d.Start.Line+1, // Convert 0-based to 1-based
			d.Start.Character+1,
			typeStr,
			d.Message,
		))
	}

	// Summary line
	sb.WriteString(fmt.Sprintf("\nsvelte-check: %d errors, %d warnings (%d files checked)\n",
		event.ErrorCount, event.WarningCount, event.FileCount))

	return sb.String()
}
