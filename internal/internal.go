// Package internal contains the core logic for svelte-check-server.
package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tylergannon/go-signal"
	kexec "k8s.io/utils/exec"
)

// =============================================================================
// Watcher Limit
// =============================================================================

// MaxWatchers is the global limit on the number of filesystem watchers.
// If this limit is exceeded, creating new watchers will fail with ErrTooManyWatchers.
// This prevents misconfiguration from exhausting OS resources.
const MaxWatchers = 100

// globalWatcherCount tracks the number of active filesystem watchers.
var globalWatcherCount atomic.Int32

// ErrTooManyWatchers is returned when attempting to create a watcher would exceed MaxWatchers.
var ErrTooManyWatchers = errors.New("too many filesystem watchers: limit exceeded")

// WatcherCount returns the current number of active watchers.
func WatcherCount() int32 {
	return globalWatcherCount.Load()
}

// acquireWatcher attempts to increment the watcher count.
// Returns an error if the limit would be exceeded.
func acquireWatcher() error {
	for {
		current := globalWatcherCount.Load()
		if current >= MaxWatchers {
			return ErrTooManyWatchers
		}
		if globalWatcherCount.CompareAndSwap(current, current+1) {
			return nil
		}
	}
}

// releaseWatcher decrements the watcher count.
func releaseWatcher() {
	globalWatcherCount.Add(-1)
}

// =============================================================================
// Socket Path
// =============================================================================

// SocketPathForWorkspace returns the socket path for a given workspace directory.
// The path is /tmp/<path-slug>-svelte-check.sock where path-slug is the
// workspace path with slashes replaced by dashes.
func SocketPathForWorkspace(workspacePath string) (string, error) {
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}

	absPath = filepath.Clean(absPath)
	slug := strings.TrimPrefix(absPath, string(os.PathSeparator))
	slug = strings.ReplaceAll(slug, string(os.PathSeparator), "-")

	return filepath.Join(os.TempDir(), slug+"-svelte-check.sock"), nil
}

// SocketExists checks if a socket file exists at the given path.
func SocketExists(socketPath string) bool {
	_, err := os.Stat(socketPath)
	return err == nil
}

// =============================================================================
// Runner
// =============================================================================

// Runner manages a svelte-check --watch process.
type Runner struct {
	workspacePath string
	tsconfigPath  string
	executor      kexec.Interface
	cmd           kexec.Cmd

	// Holds the latest completed check result.
	// Readers block while a check is in progress.
	latest *signal.Signal[SvelteWatchCheckComplete]
}

// NewRunner creates a new Runner for the given workspace.
func NewRunner(workspacePath, tsconfigPath string, executor kexec.Interface) *Runner {
	return &Runner{
		workspacePath: workspacePath,
		tsconfigPath:  tsconfigPath,
		executor:      executor,
		latest:        signal.New[SvelteWatchCheckComplete](),
	}
}

// Start begins the svelte-check --watch process.
func (r *Runner) Start(ctx context.Context) error {
	args := []string{"run", "svelte-check", "--watch", "--output", "machine-verbose"}
	if r.tsconfigPath != "" {
		args = append(args, "--tsconfig", r.tsconfigPath)
	}

	r.cmd = r.executor.CommandContext(ctx, "bun", args...)
	r.cmd.SetDir(r.workspacePath)

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := r.cmd.Start(); err != nil {
		return err
	}

	// Wait for the process in a goroutine. This ensures ProcessState is populated
	// when the process exits, which is required for kexec's Stop() to work correctly.
	go func() {
		_ = r.cmd.Wait()
	}()

	// Combine stdout and stderr into a single reader for the interpreter
	combined := io.MultiReader(stdout, stderr)
	events := make(chan SvelteCheckEvent)

	go func() {
		if err := InterpretOutput(combined, events); err != nil {
			log.Printf("Interpreter error: %v", err)
		}
		close(events)
	}()

	go r.handleEvents(events)

	return nil
}

// Stop terminates the svelte-check process.
func (r *Runner) Stop() {
	if r.cmd != nil {
		r.cmd.Stop()
	}
}

// Restart stops and starts the svelte-check process.
func (r *Runner) Restart(ctx context.Context) error {
	r.Stop()
	time.Sleep(100 * time.Millisecond)

	// Invalidate so readers block until the new check completes
	r.latest.Invalidate()

	return r.Start(ctx)
}

// GetLatestEvent blocks until a check is complete and returns the result.
// If a check is in progress, this blocks until it completes.
func (r *Runner) GetLatestEvent() SvelteWatchCheckComplete {
	return r.latest.Get()
}

// handleEvents processes events from the interpreter and updates the Signal.
func (r *Runner) handleEvents(events <-chan SvelteCheckEvent) {
	for event := range events {
		switch e := event.(type) {
		case SvelteWatchCheckStart:
			r.latest.Invalidate()
			log.Println("svelte-check started")
		case SvelteWatchCheckComplete:
			r.latest.Set(e)
			log.Printf("svelte-check completed: %d errors, %d warnings", e.ErrorCount, e.WarningCount)
		case SvelteWatchFailure:
			log.Printf("svelte-check failure: %s", e.Message)
		}
	}
}

// RunSvelteKitSync runs `bun run svelte-kit sync` to regenerate types.
// This should be called when route files are created, deleted, or renamed.
func RunSvelteKitSync(ctx context.Context, workspacePath string, executor kexec.Interface) error {
	cmd := executor.CommandContext(ctx, "bun", "run", "svelte-kit", "sync")
	cmd.SetDir(workspacePath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("svelte-kit sync failed: %w\n%s", err, string(output))
	}
	return nil
}

// RunOnce runs svelte-check once (non-watch mode) and returns the exit code.
func RunOnce(ctx context.Context, workspacePath, tsconfigPath string, executor kexec.Interface) (output string, exitCode int) {
	args := []string{"run", "svelte-check"}
	if tsconfigPath != "" {
		args = append(args, "--tsconfig", tsconfigPath)
	}

	cmd := executor.CommandContext(ctx, "bun", args...)
	cmd.SetDir(workspacePath)

	out, err := cmd.CombinedOutput()
	output = string(out)

	if err != nil {
		if exitErr, ok := err.(kexec.ExitError); ok {
			return output, exitErr.ExitStatus()
		}
		return output, 1
	}
	return output, 0
}

// =============================================================================
// Server
// =============================================================================

// Server is an HTTP server over UDS that exposes svelte-check state.
type Server struct {
	socketPath string
	runner     *Runner
	httpServer *http.Server
	mu         sync.Mutex
	shutdownCh chan struct{}
}

// NewServer creates a new Server.
func NewServer(socketPath string, runner *Runner) *Server {
	return &Server{
		socketPath: socketPath,
		runner:     runner,
		shutdownCh: make(chan struct{}),
	}
}

// Start begins listening on the Unix socket.
func (s *Server) Start() error {
	_ = os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /check", s.handleCheck)
	mux.HandleFunc("POST /stop", s.handleStop)

	s.httpServer = &http.Server{Handler: mux}

	go func() { _ = s.httpServer.Serve(listener) }()

	return nil
}

// Stop gracefully shuts down the server and removes the socket file.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.httpServer != nil {
		err = s.httpServer.Shutdown(ctx)
	}
	_ = os.Remove(s.socketPath)
	return err
}

// SocketPath returns the path to the Unix socket.
func (s *Server) SocketPath() string {
	return s.socketPath
}

// ShutdownCh returns a channel that closes when shutdown is requested via HTTP.
func (s *Server) ShutdownCh() <-chan struct{} {
	return s.shutdownCh
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	event := s.runner.GetLatestEvent()

	// Check for format query parameter: ?format=json or ?format=human (default)
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "human"
	}

	if event.ErrorCount > 0 {
		w.WriteHeader(http.StatusInternalServerError)
	}

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(event)
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(FormatHuman(event)))
	}
}

func (s *Server) handleStop(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	go func() { close(s.shutdownCh) }()
}

// =============================================================================
// Client
// =============================================================================

// Client communicates with the svelte-check server.
type Client struct {
	socketPath string
	httpClient *http.Client
}

// NewClient creates a new Client for the given workspace.
func NewClient(workspacePath string) (*Client, error) {
	socketPath, err := SocketPathForWorkspace(workspacePath)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	return &Client{
		socketPath: socketPath,
		httpClient: httpClient,
	}, nil
}

// IsServerRunning checks if the server is running.
func (c *Client) IsServerRunning() bool {
	return SocketExists(c.socketPath)
}

// Check retrieves the latest check result from the server.
// Blocks if a check is currently in progress.
// format can be "human" or "json".
// Returns the output, whether there were errors, and any error communicating with server.
func (c *Client) Check(ctx context.Context, format string) (output string, hasErrors bool, err error) {
	url := "http://unix/check"
	if format != "" && format != "human" {
		url = fmt.Sprintf("http://unix/check?format=%s", format)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, err
	}

	output = string(body)
	hasErrors = resp.StatusCode == http.StatusInternalServerError
	return output, hasErrors, nil
}

// SocketPath returns the socket path for this client.
func (c *Client) SocketPath() string {
	return c.socketPath
}

// Stop requests the server to shut down gracefully via HTTP.
func (c *Client) Stop(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "POST", "http://unix/stop", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return nil
}

// =============================================================================
// Watcher
// =============================================================================

// FSWatcher abstracts filesystem watching for testability.
type FSWatcher interface {
	Events() <-chan fsnotify.Event
	Errors() <-chan error
	Add(path string, recursive bool) error
	Rescan() error
	Close() error
}

// RealFSWatcher wraps fsnotify.Watcher to implement FSWatcher.
type RealFSWatcher struct {
	watcher *fsnotify.Watcher
	paths   []watchedPath // track paths for Rescan
	mu      sync.Mutex
}

type watchedPath struct {
	path      string
	recursive bool
}

// NewRealFSWatcher creates a new RealFSWatcher.
// Returns ErrTooManyWatchers if the global watcher limit would be exceeded.
func NewRealFSWatcher() (*RealFSWatcher, error) {
	if err := acquireWatcher(); err != nil {
		return nil, err
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		releaseWatcher()
		return nil, err
	}
	return &RealFSWatcher{watcher: w}, nil
}

func (r *RealFSWatcher) Events() <-chan fsnotify.Event {
	return r.watcher.Events
}

func (r *RealFSWatcher) Errors() <-chan error {
	return r.watcher.Errors
}

func (r *RealFSWatcher) Add(path string, recursive bool) error {
	r.mu.Lock()
	r.paths = append(r.paths, watchedPath{path: path, recursive: recursive})
	r.mu.Unlock()

	if recursive {
		return r.addRecursive(path)
	}
	return r.watcher.Add(path)
}

func (r *RealFSWatcher) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if err := r.watcher.Add(path); err != nil {
				log.Printf("Warning: could not watch %s: %v", path, err)
			}
		}
		return nil
	})
}

func (r *RealFSWatcher) Rescan() error {
	r.mu.Lock()
	paths := make([]watchedPath, len(r.paths))
	copy(paths, r.paths)
	r.mu.Unlock()

	for _, wp := range paths {
		if wp.recursive {
			if err := r.addRecursive(wp.path); err != nil {
				log.Printf("Warning: rescan failed for %s: %v", wp.path, err)
			}
		}
	}
	return nil
}

func (r *RealFSWatcher) Close() error {
	releaseWatcher()
	return r.watcher.Close()
}

// GitBranchWatcher watches for git branch changes and emits events on channels.
type GitBranchWatcher interface {
	HeadChanged() <-chan struct{}   // emits when HEAD changes (branch switch)
	BranchChanged() <-chan struct{} // emits when current branch ref changes (commit/pull/etc)
	Start(ctx context.Context)      // blocks until context is cancelled
	Close() error
}

// RealGitBranchWatcher implements GitBranchWatcher using fsnotify.
type RealGitBranchWatcher struct {
	workspacePath string
	executor      kexec.Interface
	watcher       *fsnotify.Watcher
	headCh        chan struct{}
	branchCh      chan struct{}
	gitRoot       string
	gitDir        string
}

// NewRealGitBranchWatcher creates a new RealGitBranchWatcher for the given workspace.
// Returns ErrTooManyWatchers if the global watcher limit would be exceeded.
func NewRealGitBranchWatcher(workspacePath string, executor kexec.Interface) (*RealGitBranchWatcher, error) {
	if err := acquireWatcher(); err != nil {
		return nil, err
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		releaseWatcher()
		return nil, err
	}

	r := &RealGitBranchWatcher{
		workspacePath: workspacePath,
		executor:      executor,
		watcher:       w,
		headCh:        make(chan struct{}, 1),
		branchCh:      make(chan struct{}, 1),
	}
	r.gitRoot = r.findGitRoot()
	if r.gitRoot != "" {
		r.gitDir = filepath.Join(r.gitRoot, ".git")
	}
	return r, nil
}

func (r *RealGitBranchWatcher) findGitRoot() string {
	cmd := r.executor.Command("git", "rev-parse", "--show-toplevel")
	cmd.SetDir(r.workspacePath)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (r *RealGitBranchWatcher) HeadChanged() <-chan struct{} {
	return r.headCh
}

func (r *RealGitBranchWatcher) BranchChanged() <-chan struct{} {
	return r.branchCh
}

// Start begins watching git files. This blocks until the context is cancelled.
func (r *RealGitBranchWatcher) Start(ctx context.Context) {
	if r.gitDir == "" {
		// Not a git repo, just block until context is cancelled
		<-ctx.Done()
		return
	}

	headPath := filepath.Join(r.gitDir, "HEAD")
	if err := r.watcher.Add(headPath); err != nil {
		log.Printf("Warning: could not watch .git/HEAD: %v", err)
	} else {
		log.Printf("Watching %s for branch switches", headPath)
	}

	// Watch current branch ref
	currentBranchRefPath := r.currentBranchRefPath()
	if currentBranchRefPath != "" {
		if err := r.watcher.Add(currentBranchRefPath); err != nil {
			log.Printf("Warning: could not watch branch ref: %v", err)
		} else {
			log.Printf("Watching %s for branch updates", currentBranchRefPath)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-r.watcher.Events:
			if !ok {
				return
			}

			if event.Name == headPath {
				log.Println("Git HEAD changed (branch switch)")
				// Update watch for new branch ref
				newBranchRefPath := r.currentBranchRefPath()
				if newBranchRefPath != "" && newBranchRefPath != currentBranchRefPath {
					if err := r.watcher.Add(newBranchRefPath); err == nil {
						log.Printf("Now watching %s for branch updates", newBranchRefPath)
						currentBranchRefPath = newBranchRefPath
					}
				}
				// Non-blocking send
				select {
				case r.headCh <- struct{}{}:
				default:
				}
				continue
			}

			// Check if this is a branch ref update (any file in .git/refs/heads/)
			if strings.HasPrefix(event.Name, filepath.Join(r.gitDir, "refs", "heads")) {
				log.Println("Branch ref updated (commit/pull/merge/rebase)")
				// Non-blocking send
				select {
				case r.branchCh <- struct{}{}:
				default:
				}
				continue
			}

		case err, ok := <-r.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Git watcher error: %v", err)
		}
	}
}

func (r *RealGitBranchWatcher) currentBranchRefPath() string {
	if r.gitDir == "" {
		return ""
	}
	headPath := filepath.Join(r.gitDir, "HEAD")
	content, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}

	ref := parseGitHeadRef(string(content))
	if ref == "" {
		return ""
	}
	return filepath.Join(r.gitDir, ref)
}

// parseGitHeadRef parses the content of a .git/HEAD file and returns the ref path.
// Returns empty string for detached HEAD (raw commit SHA) or invalid content.
func parseGitHeadRef(content string) string {
	line := strings.TrimSpace(content)
	if !strings.HasPrefix(line, "ref: ") {
		return "" // detached HEAD or invalid
	}
	return strings.TrimPrefix(line, "ref: ")
}

func (r *RealGitBranchWatcher) Close() error {
	releaseWatcher()
	return r.watcher.Close()
}

// WatcherConfig holds watcher configuration.
type WatcherConfig struct {
	WorkspacePath    string
	RecursiveDirs    []string
	NonRecursiveDirs []string
}

// WatcherCallbacks holds the callback functions for the watcher.
type WatcherCallbacks struct {
	OnRestart    func() // Called when svelte-check should restart
	OnSvelteSync func() // Called when svelte-kit sync should run
}

// Watcher watches files and triggers callbacks on changes.
type Watcher struct {
	config           WatcherConfig
	fsWatcher        FSWatcher
	callbacks        WatcherCallbacks
	gitBranchWatcher GitBranchWatcher // can be nil if not a git repo

	restartDebouncer *Debouncer
	syncDebouncer    *Debouncer
}

// svelteKitRouteFiles lists all SvelteKit route files that need svelte-kit sync
// when created, deleted, or renamed. These files define load functions and
// endpoints whose types are generated by svelte-kit sync.
var svelteKitRouteFiles = map[string]bool{
	"+page.ts":          true,
	"+page.js":          true,
	"+page.server.ts":   true,
	"+page.server.js":   true,
	"+layout.ts":        true,
	"+layout.js":        true,
	"+layout.server.ts": true,
	"+layout.server.js": true,
	"+server.ts":        true,
	"+server.js":        true,
}

// isRouteFile returns true if the filename is a SvelteKit route file
// that needs svelte-kit sync when created/deleted/renamed.
func isRouteFile(filename string) bool {
	return svelteKitRouteFiles[filepath.Base(filename)]
}

// NewWatcher creates a new Watcher with the given configuration.
// gitBranchWatcher can be nil if not watching a git repository.
func NewWatcher(config WatcherConfig, callbacks WatcherCallbacks, fsWatcher FSWatcher, gitBranchWatcher GitBranchWatcher) *Watcher {
	const debounceInterval = 250 * time.Millisecond
	return &Watcher{
		config:           config,
		fsWatcher:        fsWatcher,
		callbacks:        callbacks,
		gitBranchWatcher: gitBranchWatcher,
		restartDebouncer: NewDebouncer(debounceInterval, callbacks.OnRestart),
		syncDebouncer:    NewDebouncer(debounceInterval, callbacks.OnSvelteSync),
	}
}

// Start begins watching files. This blocks until the context is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	for _, dir := range w.config.NonRecursiveDirs {
		absDir := filepath.Join(w.config.WorkspacePath, dir)
		if err := w.fsWatcher.Add(absDir, false); err != nil {
			log.Printf("Warning: could not watch %s: %v", absDir, err)
		}
	}

	for _, dir := range w.config.RecursiveDirs {
		absDir := filepath.Join(w.config.WorkspacePath, dir)
		if err := w.fsWatcher.Add(absDir, true); err != nil {
			log.Printf("Warning: could not watch %s recursively: %v", absDir, err)
		}
	}

	// Get git channels (may be nil if no git watcher)
	var headCh, branchCh <-chan struct{}
	if w.gitBranchWatcher != nil {
		headCh = w.gitBranchWatcher.HeadChanged()
		branchCh = w.gitBranchWatcher.BranchChanged()
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-headCh:
			log.Println("Git HEAD changed (branch switch), restarting svelte-check...")
			w.restartDebouncer.Trigger()

		case <-branchCh:
			log.Println("Branch ref updated (commit/pull/merge/rebase), restarting svelte-check...")
			w.restartDebouncer.Trigger()

		case event, ok := <-w.fsWatcher.Events():
			if !ok {
				return
			}

			// Check if this is a SvelteKit route file change
			if isRouteFile(event.Name) {
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					log.Printf("Route file changed: %s, running svelte-kit sync...", filepath.Base(event.Name))
					w.syncDebouncer.Trigger()
				}
			}

			// Handle new directories - rescan to pick up new subdirectories
			if event.Has(fsnotify.Create) {
				_ = w.fsWatcher.Rescan()
			}

		case err, ok := <-w.fsWatcher.Errors():
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	w.restartDebouncer.Stop()
	w.syncDebouncer.Stop()
	return w.fsWatcher.Close()
}
