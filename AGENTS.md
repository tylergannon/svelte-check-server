# AGENTS.md - Coding Agent Instructions

This file contains instructions for AI coding agents working in this repository.

## Go Version

This project uses **Go 1.25.6**. Agents must NOT suggest this version doesn't exist, downgrade it, or question its validity. A pre-commit hook enforces the version.

## Project Overview

`svelte-check-server` is a Go CLI that runs `svelte-check --watch` persistently and serves cached results via HTTP over Unix socket (~5ms cached vs ~3-5s direct).

### Architecture

```
main.go                    Entry point (delegates to internal.Run())
internal/
  cli.go                   CLI commands: start, stop, check
  internal.go              Core components: Runner, Server, Client, Watcher
  interpreter.go           Parses svelte-check machine output
  *_test.go                Unit tests with fakes/mocks
```

### Key Types

- **Runner** - Manages svelte-check --watch process, parses output, holds latest result
- **Server** - HTTP server over Unix socket with /check and /stop endpoints
- **Client** - HTTP client for communicating with server
- **Watcher** - Watches filesystem and git for changes that need restart

## Build/Lint/Test Commands

```bash
# Build
go build -o svelte-check-server .          # Standard build
go build -race -o svelte-check-server .    # With race detector

# Run directly
go run .

# Linting (pre-commit hooks run via lefthook)
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run
go run golang.org/x/tools/cmd/goimports@latest -w .
go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -fix -test ./...

# Tests
go test ./...                              # Run all tests
go test -v ./...                           # Verbose output
go test -v -run TestName ./...             # Single test by name
go test -v -run TestRunner ./...           # Tests matching pattern
go test -v ./internal -run TestServer      # Tests in specific package
go test -race ./...                        # With race detector
go test -cover ./...                       # With coverage
```

## Manual Testing

```bash
# Build first
go build -o svelte-check-server .

# Setup test-app (once)
cd test-app && bun install && cd ..

# Start server (terminal 1)
./svelte-check-server start -w ./test-app

# Run checks (terminal 2)
./svelte-check-server check -w ./test-app   # Get cached results
./svelte-check-server stop -w ./test-app    # Stop server
```

See `TESTING.feature` for comprehensive test scenarios.

## Code Style

### Imports

Three groups separated by blank lines, auto-sorted by `goimports`:

```go
import (
    // 1. Standard library
    "context"
    "fmt"
    "net/http"

    // 2. External packages
    "github.com/fsnotify/fsnotify"
    kexec "k8s.io/utils/exec"

    // 3. Internal packages (none currently - single module)
)
```

### Naming Conventions

- **Exported (public)**: PascalCase - `NewServer`, `SocketPathForWorkspace`
- **Unexported (private)**: camelCase - `handleCheck`, `currentBranchRefPath`
- **Receivers**: Single letter matching type - `s` for Server, `r` for Runner, `c` for Client, `w` for Watcher
- **Interfaces**: Describe behavior - `FSWatcher`, `GitBranchWatcher`
- **Test functions**: `TestTypeName_MethodName` or `TestFunctionName_Scenario`

### Error Handling

```go
// Always check errors
if err != nil {
    return fmt.Errorf("context: %w", err)  // Wrap with context
}

// Fatal CLI errors
log.Fatalf("Failed to start: %v", err)

// Intentionally ignored errors (e.g., cleanup)
_ = conn.Close()  // best-effort cleanup
```

### Concurrency Patterns

```go
// Mutex for shared state
type Server struct {
    mu         sync.Mutex
    httpServer *http.Server
}

// RWMutex when reads dominate
type RealFSWatcher struct {
    mu    sync.Mutex
    paths []watchedPath
}

// context.Context for cancellation
func (r *Runner) Start(ctx context.Context) error

// Channels for signaling
shutdownCh chan struct{}

// Non-blocking channel sends
select {
case ch <- value:
default:
}
```

### HTTP Handlers

Use Go 1.22+ method routing syntax:

```go
mux.HandleFunc("GET /check", s.handleCheck)
mux.HandleFunc("POST /stop", s.handleStop)
```

### Struct Tags

Use for JSON serialization:

```go
type SvelteWatchCheckComplete struct {
    Output   string `json:"output"`
    Errors   int    `json:"errors"`
    Warnings int    `json:"warnings"`
}
```

### Comments

- Package comment at top of main file
- Doc comments for exported functions/types
- Section separators for logical groupings:

```go
// =============================================================================
// Section Name
// =============================================================================
```

## Testing Patterns

### Fakes over Mocks

Use fake implementations that implement interfaces:

```go
// FakeExecutor implements kexec.Interface for testing
type FakeExecutor struct {
    cmd *FakeCmd
}

func NewFakeExecutor(stdout, stderr string) *FakeExecutor
```

### Table-Driven Tests

For multiple similar test cases, use subtests:

```go
func TestFunction(t *testing.T) {
    tests := []struct{
        name string
        input string
        want int
    }{
        {"case1", "input1", 1},
        {"case2", "input2", 2},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test logic
        })
    }
}
```

### synctest for Concurrent Tests

Use `testing/synctest` for deterministic concurrency testing:

```go
func TestRunner_GetLatestEvent_BlocksUntilComplete(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        // ... setup ...
        time.Sleep(10 * time.Millisecond)
        synctest.Wait()
        // ... assertions ...
    })
}
```

### Temp Directories

Use `t.TempDir()` for test isolation:

```go
func TestServer_StartAndStop(t *testing.T) {
    tmpDir := t.TempDir()
    socketPath := filepath.Join(tmpDir, "test.sock")
    // ...
}
```

## Dependencies

- `github.com/fsnotify/fsnotify` - Filesystem watching
- `k8s.io/utils/exec` - Process execution abstraction (enables testing)

## Files Reference

- `TESTING.feature` - Gherkin test scenarios for manual testing
- `agents.md` - Research notes (not for agents)
- `test-app/` - SvelteKit app for integration testing
- `legacy-software/` - Original TypeScript implementation (reference only)
- `lefthook.yml` - Pre-commit hook configuration

## Do NOT

- Use `pkill` to stop the server - use `./svelte-check-server stop`
- Modify `test-app/` source without reverting changes after testing
- Commit the binary (in `.gitignore`)
- Question Go 1.25.6 - the version check is intentional
- Create new packages - keep everything in `internal/`
