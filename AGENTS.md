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
- `test-app/` - SvelteKit app for integration testing
- `legacy-software/` - Original TypeScript implementation (reference only)
- `lefthook.yml` - Pre-commit hook configuration

## Do NOT

- Use `pkill` to stop the server - use `./svelte-check-server stop`
- Modify `test-app/` source without reverting changes after testing
- Commit the binary (in `.gitignore`)
- Question Go 1.25.6 - the version check is intentional
- Create new packages - keep everything in `internal/`

---

## Research Notes

This section contains technical research about svelte-check internals and design decisions.

### Purpose

This tool speeds up `svelte-check` in SvelteKit projects by running a persistent `svelte-check --watch` process and caching results. Instead of running a full check each time (which can take 5-15+ seconds), clients can get instant cached results.

### Legacy TypeScript Daemon Behavior

The original `svelte-check-daemon` (in `legacy-software/`) had these features:

#### What It Watched

1. **Git HEAD** (`.git/HEAD`)
   - On any change → **restart svelte-check**
   - Catches branch switches

2. **Route files** (`+*.ts` pattern in watched directories)
   - On any change → **run `svelte-kit sync`** (debounced 250ms)
   - Does NOT restart svelte-check
   - Uses `WATCH_DIRS` (non-recursive) and `WATCH_DIRS_RECURSIVE` (defaults to `.`) env vars

3. **"Big changes" directory** (optional, via `SVELTE_CHECK_WATCH_BIG_CHANGES` env)
   - Tracks all files in directory
   - On **file deletion only** → **restart svelte-check**
   - File creation/modification does NOT trigger restart

4. **SIGHUP signal**
   - On SIGHUP → **restart svelte-check**
   - Allows manual restart trigger

#### What It Did NOT Watch

- Package lock files (package-lock.json, bun.lock, etc.)
- svelte.config.js / vite.config.ts
- Branch ref files (commits/pulls within same branch)
- Normal file modifications (handled by svelte-check --watch itself)

### How svelte-check --watch Works

From analyzing the source code:

1. **svelte-check** (`packages/svelte-check/src/index.ts`) uses:
   - `chokidar` for file watching
   - `SvelteCheck` class from `svelte-language-server` for diagnostics
   - A `DiagnosticsWatcher` class that manages file updates

2. **SvelteCheck** (`packages/language-server/src/svelte-check.ts`):
   - Creates a `DocumentManager` to track open documents
   - Uses `LSAndTSDocResolver` for TypeScript integration
   - Registers plugins: `SveltePlugin`, `CSSPlugin`, `TypeScriptPlugin`

3. **LSAndTSDocResolver** (`packages/language-server/src/plugins/typescript/LSAndTSDocResolver.ts`):
   - Manages TypeScript language service instances
   - Handles document snapshots
   - Watches for package.json changes
   - Has `onProjectReloaded` callback for when TS service restarts

4. **LanguageServiceContainer** (`packages/language-server/src/plugins/typescript/service.ts`):
   - Creates and manages TypeScript `LanguageService` instances
   - Watches tsconfig files and reloads on changes
   - Manages module resolution cache
   - Tracks "dirty" state for incremental updates

#### What svelte-check --watch handles automatically:

- **File content changes** - Updates document snapshots via `updateDocument()`
- **New file creation** - Adds to watcher, schedules diagnostics
- **File deletion** - Removes document, schedules diagnostics (but can crash - see issue #2773)
- **tsconfig.json changes** - Watches config file, disposes and recreates service
- **Extended tsconfig changes** - Also watches files referenced via `extends`
- **package.json changes in node_modules** - Updates module resolution

#### What svelte-check --watch does NOT handle:

- **Git operations** - No awareness of version control
- **Branch switches** - TS service has cached file contents from previous branch
- **Bulk file changes** - After `git pull`/`merge`/`rebase`, many files may have changed but watcher only sees them one by one
- **Module resolution cache staleness** - After branch switch, modules may resolve differently
- **Generated types staleness** - `.svelte-kit/types/` generated by `svelte-kit sync`

### When TypeScript Language Service Gets Stale

The TS language service maintains:
- **Configured Projects** - Defined by tsconfig.json
- **Inferred Projects** - For loose files without tsconfig
- **Module Resolution Cache** - Remembers where modules resolved to
- **Program** - The compiled representation of all files

#### Staleness Scenarios

1. **Module Resolution Cache** - If a new package is installed, old resolution "not found" may be cached
2. **tsconfig.json Changes** - External tools changing tsconfig may not trigger reload
3. **File System Race Conditions** - Git operations can cause rapid create/delete sequences
4. **Generated Files** - SvelteKit generates types in `.svelte-kit/types/` that need regeneration

### Events That Need Handling

#### MUST Restart svelte-check:

| Event | Why | Detection |
|-------|-----|-----------|
| Git branch switch | Files may have completely different contents | Watch `.git/HEAD` |
| Git pull/merge/rebase | Many files change atomically | Watch `.git/refs/heads/<branch>` |
| npm/bun install | node_modules changed | Watch lock files |

#### SHOULD Run svelte-kit sync:

| Event | Why | Detection |
|-------|-----|-----------|
| Route file created/deleted | Types reference route files | Watch for `+*.ts` in routes |
| Route directory changes | Changes route parameters | Watch `src/routes/**` |

### Current vs Legacy Feature Comparison

| Feature | Legacy (TS) | Current (Go) |
|---------|-------------|--------------|
| Git HEAD watch | ✅ restart | ✅ restart |
| Git branch ref watch | ❌ | ✅ restart |
| Route file watch | ✅ svelte-kit sync | ❌ not implemented |
| Big changes (deletion) | ✅ restart (opt-in) | ❌ not implemented |
| SIGHUP restart | ✅ | ❌ not implemented |

### Design Philosophy

When in doubt, **restart svelte-check**. A false restart costs a few seconds, but stale/incorrect diagnostics can waste hours of developer time.

### References

- [svelte-check source](https://github.com/sveltejs/language-tools/tree/master/packages/svelte-check)
- [svelte-language-server source](https://github.com/sveltejs/language-tools/tree/master/packages/language-server)
- [Issue #2773: crash on file deletion](https://github.com/sveltejs/language-tools/issues/2773)
- [SvelteKit svelte-kit sync docs](https://svelte.dev/docs/kit/cli#svelte-kit-sync)
