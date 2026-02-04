package internal

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	kexec "k8s.io/utils/exec"
)

// stringSlice is a flag.Value that collects multiple -r or -d flags.
type stringSlice []string

func (s *stringSlice) String() string {
	return fmt.Sprintf("%v", *s)
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// Run is the main entry point for the CLI.
func Run() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "start":
		cmdStart(args)
	case "check":
		cmdCheck(args)
	case "stop":
		cmdStop(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`svelte-check-server - Fast svelte-check with persistent watch process

Usage:
  svelte-check-server <command> [options]

Commands:
  start     Start the server (runs svelte-check --watch in background)
  check     Get check results (falls back to direct execution if server not running)
  stop      Stop the server

Options for 'start':
  -w, --workspace <path>   Working directory (default: current directory)
  -r <dir>                 Add recursive watch directory (can be repeated)
  -d <dir>                 Add non-recursive watch directory (can be repeated)
  --tsconfig <path>        Path to tsconfig.json

Options for 'check':
  -w, --workspace <path>   Working directory (default: current directory)
  --tsconfig <path>        Path to tsconfig.json
  --format <human|json>    Output format (default: human)
  --timeout <duration>     Timeout waiting for check to complete (default: 2m)

Defaults:
  - Watch '.' non-recursively
  - Watch './src' recursively
  - Watch '.git/HEAD' and current branch ref for git changes`)
}

func cmdStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)

	var workspace string
	var tsconfig string
	var recursiveDirs stringSlice
	var nonRecursiveDirs stringSlice

	fs.StringVar(&workspace, "w", ".", "Working directory")
	fs.StringVar(&workspace, "workspace", ".", "Working directory")
	fs.StringVar(&tsconfig, "tsconfig", "", "Path to tsconfig.json")
	fs.Var(&recursiveDirs, "r", "Recursive watch directory (can be repeated)")
	fs.Var(&nonRecursiveDirs, "d", "Non-recursive watch directory (can be repeated)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if len(recursiveDirs) == 0 && len(nonRecursiveDirs) == 0 {
		nonRecursiveDirs = []string{"."}
		recursiveDirs = []string{"./src"}
	}

	if workspace == "." {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get working directory: %v", err)
		}
	}

	socketPath, err := SocketPathForWorkspace(workspace)
	if err != nil {
		log.Fatalf("Failed to get socket path: %v", err)
	}

	if SocketExists(socketPath) {
		log.Fatalf("Server already running (socket exists at %s)", socketPath)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the real executor for production use
	executor := kexec.New()

	r := NewRunner(workspace, tsconfig, executor)
	if err := r.Start(ctx); err != nil {
		log.Fatalf("Failed to start svelte-check: %v", err)
	}

	srv := NewServer(socketPath, r)
	if err := srv.Start(); err != nil {
		r.Stop()
		log.Fatalf("Failed to start server: %v", err)
	}

	watcherConfig := WatcherConfig{
		WorkspacePath:    workspace,
		RecursiveDirs:    recursiveDirs,
		NonRecursiveDirs: nonRecursiveDirs,
	}

	callbacks := WatcherCallbacks{
		OnRestart: func() {
			log.Println("File change detected, restarting svelte-check...")
			if err := r.Restart(ctx); err != nil {
				log.Printf("Failed to restart svelte-check: %v", err)
			}
		},
		OnSvelteSync: func() {
			log.Println("Running svelte-kit sync...")
			if err := RunSvelteKitSync(ctx, workspace, executor); err != nil {
				log.Printf("svelte-kit sync failed: %v", err)
			} else {
				log.Println("svelte-kit sync completed")
			}
		},
	}

	fsWatcher, err := NewRealFSWatcher()
	if err != nil {
		_ = srv.Stop(ctx)
		r.Stop()
		log.Fatalf("Failed to create filesystem watcher: %v", err)
	}

	gitBranchWatcher, err := NewRealGitBranchWatcher(workspace, executor)
	if err != nil {
		_ = srv.Stop(ctx)
		r.Stop()
		log.Fatalf("Failed to create git branch watcher: %v", err)
	}

	w := NewWatcher(watcherConfig, callbacks, fsWatcher, gitBranchWatcher)

	// Start git branch watcher in background
	go gitBranchWatcher.Start(ctx)

	go w.Start(ctx)

	log.Printf("Server started on %s", socketPath)
	log.Printf("Watching directories: %v (non-recursive), %v (recursive)", nonRecursiveDirs, recursiveDirs)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
	case <-srv.ShutdownCh():
	}

	log.Println("Shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = w.Close()
	_ = gitBranchWatcher.Close()
	r.Stop()
	if err := srv.Stop(shutdownCtx); err != nil {
		log.Printf("Error stopping server: %v", err)
	}

	log.Println("Server stopped")
}

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)

	var workspace string
	var tsconfig string
	var timeout time.Duration
	var format string

	fs.StringVar(&workspace, "w", ".", "Working directory")
	fs.StringVar(&workspace, "workspace", ".", "Working directory")
	fs.StringVar(&tsconfig, "tsconfig", "", "Path to tsconfig.json")
	fs.DurationVar(&timeout, "timeout", 120*time.Second, "Timeout waiting for check to complete")
	fs.StringVar(&format, "format", "human", "Output format: human or json")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if workspace == "." {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get working directory: %v", err)
		}
	}

	ctx := context.Background()

	c, err := NewClient(workspace)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	if !c.IsServerRunning() {
		log.Println("Server not running, running svelte-check directly...")
		executor := kexec.New()
		output, exitCode := RunOnce(ctx, workspace, tsconfig, executor)
		fmt.Print(output)
		os.Exit(exitCode)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, hasErrors, err := c.Check(ctx, format)
	if err != nil {
		log.Fatalf("Failed to get check results: %v", err)
	}

	fmt.Print(output)
	if output != "" && output[len(output)-1] != '\n' {
		fmt.Println()
	}

	if hasErrors {
		os.Exit(1)
	}
}

func cmdStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)

	var workspace string

	fs.StringVar(&workspace, "w", ".", "Working directory")
	fs.StringVar(&workspace, "workspace", ".", "Working directory")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if workspace == "." {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get working directory: %v", err)
		}
	}

	ctx := context.Background()

	c, err := NewClient(workspace)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	if !c.IsServerRunning() {
		fmt.Println("Server is not running")
		return
	}

	if err := c.Stop(ctx); err != nil {
		log.Fatalf("Failed to stop server: %v", err)
	}

	fmt.Println("Server stopped")
}
