package internal

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// testSocketPath creates a short socket path suitable for Unix domain sockets.
// macOS has a 104-character limit for socket paths, and t.TempDir() paths can
// exceed this when combined with long test names. This helper creates a socket
// directly in os.TempDir() with a unique name based on the test.
func testSocketPath(t *testing.T) string {
	t.Helper()
	path := fmt.Sprintf("%s/t%d.sock", os.TempDir(), time.Now().UnixNano())

	if len(path) > 100 {
		t.Fatalf("socket path too long (%d chars): %s", len(path), path)
	}

	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}

// TestNewServer tests the NewServer constructor.
func TestNewServer(t *testing.T) {
	executor := NewFakeExecutor("", "")
	r := NewRunner("/workspace", "", executor)
	s := NewServer("/tmp/test.sock", r)

	if s.socketPath != "/tmp/test.sock" {
		t.Errorf("socketPath = %q, want /tmp/test.sock", s.socketPath)
	}
	if s.runner != r {
		t.Error("runner not set correctly")
	}
	if s.shutdownCh == nil {
		t.Error("shutdownCh not initialized")
	}
}

// TestServer_SocketPath tests the SocketPath getter.
func TestServer_SocketPath(t *testing.T) {
	executor := NewFakeExecutor("", "")
	r := NewRunner("/workspace", "", executor)
	s := NewServer("/tmp/test.sock", r)

	if s.SocketPath() != "/tmp/test.sock" {
		t.Errorf("SocketPath() = %q, want /tmp/test.sock", s.SocketPath())
	}
}

// TestServer_StartAndStop tests starting and stopping the server.
func TestServer_StartAndStop(t *testing.T) {
	socketPath := testSocketPath(t)

	// Create a runner with output so GetLatestEvent doesn't block forever
	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	// Wait for the runner to process
	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)

	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify socket file exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Error("Socket file not created")
	}

	// Stop the server
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = s.Stop(stopCtx)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify socket file is removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("Socket file not removed after stop")
	}
}

// TestServer_HandleCheck_WithErrors tests GET /check returns 500 when there are errors.
func TestServer_HandleCheck_WithErrors(t *testing.T) {
	socketPath := testSocketPath(t)

	output := `1770255832071 START "/workspace"
1770255834342 {"type":"ERROR","filename":"src/a.ts","start":{"line":0,"character":0},"end":{"line":0,"character":1},"message":"Test error","code":2322}
1770255834342 {"type":"WARNING","filename":"src/b.ts","start":{"line":1,"character":0},"end":{"line":1,"character":1},"message":"Test warning","code":"a11y_test","source":"svelte"}
1770255834342 COMPLETED 100 FILES 1 ERRORS 1 WARNINGS 2 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = s.Stop(context.Background())
	}()

	// Create an HTTP client that connects via Unix socket
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://unix/check")
	if err != nil {
		t.Fatalf("GET /check failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Should return 500 because there are errors
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Status code = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ERROR") {
		t.Errorf("Body should contain ERROR, got: %s", body)
	}
}

// TestServer_HandleCheck_NoErrors tests GET /check returns 200 when there are no errors.
func TestServer_HandleCheck_NoErrors(t *testing.T) {
	socketPath := testSocketPath(t)

	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = s.Stop(context.Background())
	}()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://unix/check")
	if err != nil {
		t.Fatalf("GET /check failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status code = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestServer_HandleStop tests the POST /stop endpoint.
func TestServer_HandleStop(t *testing.T) {
	socketPath := testSocketPath(t)

	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post("http://unix/stop", "", nil)
	if err != nil {
		t.Fatalf("POST /stop failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status code = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Verify shutdown channel is closed
	select {
	case <-s.ShutdownCh():
		// Success
	case <-time.After(1 * time.Second):
		t.Error("ShutdownCh not closed after /stop request")
	}

	_ = s.Stop(context.Background())
}

// TestServer_ShutdownCh tests the ShutdownCh getter.
func TestServer_ShutdownCh(t *testing.T) {
	executor := NewFakeExecutor("", "")
	r := NewRunner("/workspace", "", executor)
	s := NewServer("/tmp/test.sock", r)

	ch := s.ShutdownCh()
	if ch == nil {
		t.Error("ShutdownCh() returned nil")
	}
}

// TestServer_RemovesExistingSocket tests that Start removes existing socket.
func TestServer_RemovesExistingSocket(t *testing.T) {
	socketPath := testSocketPath(t)

	// Create a dummy file at the socket path
	if err := os.WriteFile(socketPath, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}

	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)

	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = s.Stop(context.Background())
	}()

	// Verify server is working (socket was replaced)
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://unix/check")
	if err != nil {
		t.Fatalf("GET /check failed: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status code = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestSocketExists tests the SocketExists function.
func TestSocketExists(t *testing.T) {
	socketPath := testSocketPath(t)

	// Socket doesn't exist
	if SocketExists(socketPath) {
		t.Error("SocketExists returned true for non-existent socket")
	}

	// Create the socket
	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = s.Stop(context.Background())
	}()

	// Socket exists
	if !SocketExists(socketPath) {
		t.Error("SocketExists returned false for existing socket")
	}
}

// TestClient_IsServerRunning tests the IsServerRunning method.
func TestClient_IsServerRunning(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := tmpDir

	// Override socket path for testing
	client, err := NewClient(workspacePath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// No server running yet
	if client.IsServerRunning() {
		t.Error("IsServerRunning returned true when no server is running")
	}
}

// TestClient_Shutdown tests the Shutdown method.
func TestClient_Shutdown(t *testing.T) {
	socketPath := testSocketPath(t)

	output := `1770255832071 START "/workspace"
1770255834342 COMPLETED 100 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS
`
	executor := NewFakeExecutor(output, "")
	r := NewRunner("/workspace", "", executor)

	ctx := context.Background()
	_ = r.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	s := NewServer(socketPath, r)
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Create client directly with the socket path
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	client := &Client{
		socketPath: socketPath,
		httpClient: httpClient,
	}

	err = client.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify shutdown was received
	select {
	case <-s.ShutdownCh():
		// Success
	case <-time.After(1 * time.Second):
		t.Error("ShutdownCh not closed after client.Shutdown()")
	}

	_ = s.Stop(context.Background())
}
