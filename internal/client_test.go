package internal

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestCheck tests the Check method which calls GET /check.
// The server now blocks until a check is complete, so:
// - No polling needed
// - Timeout handled via context
// - Context cancellation still works
//
// We use synctest for deterministic time control and net.Pipe for
// in-memory networking (required for synctest compatibility).

// fakeCheckServer is a minimal HTTP server over net.Pipe that returns
// configurable plain text responses. It's designed to work with synctest.
// The server blocks until a response is available (simulating server-side blocking).
type fakeCheckServer struct {
	conn      net.Conn
	responses chan checkResponse // send responses here, server will return them
}

type checkResponse struct {
	output    string
	hasErrors bool
}

func newFakeCheckServer(conn net.Conn) *fakeCheckServer {
	return &fakeCheckServer{
		conn:      conn,
		responses: make(chan checkResponse, 10),
	}
}

func (s *fakeCheckServer) serve(ctx context.Context) {
	reader := bufio.NewReader(s.conn)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Read the HTTP request
		req, err := http.ReadRequest(reader)
		if err != nil {
			return // Connection closed
		}
		_ = req.Body.Close()

		// Get the next response to return (blocks until available)
		select {
		case resp := <-s.responses:
			status := "200 OK"
			if resp.hasErrors {
				status = "500 Internal Server Error"
			}
			httpResp := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s", status, len(resp.output), resp.output)
			_, _ = s.conn.Write([]byte(httpResp))
		case <-ctx.Done():
			return
		}
	}
}

// createTestClient creates a Client that uses net.Pipe instead of a real socket.
// This is synctest-compatible because net.Pipe doesn't involve real I/O.
func createTestClient(clientConn net.Conn) *Client {
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return clientConn, nil
			},
		},
		Timeout: 30 * time.Second,
	}

	return &Client{
		socketPath: "/fake/socket.sock",
		httpClient: httpClient,
	}
}

func TestCheck_ReturnsImmediatelyWhenAvailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		server := newFakeCheckServer(serverConn)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.serve(ctx)

		client := createTestClient(clientConn)

		// Queue a successful response
		server.responses <- checkResponse{output: "all good", hasErrors: false}

		start := time.Now()
		output, hasErrors, err := client.Check(ctx, "human")
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		if output != "all good" {
			t.Errorf("output = %q, want 'all good'", output)
		}
		if hasErrors {
			t.Errorf("hasErrors = true, want false")
		}
		// Should return almost immediately
		if elapsed >= 100*time.Millisecond {
			t.Errorf("Should return immediately, took %v", elapsed)
		}
	})
}

func TestCheck_ReturnsHasErrorsOn500(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		server := newFakeCheckServer(serverConn)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.serve(ctx)

		client := createTestClient(clientConn)

		// Queue an error response (500)
		server.responses <- checkResponse{output: "ERROR in file.ts", hasErrors: true}

		output, hasErrors, err := client.Check(ctx, "human")

		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		if output != "ERROR in file.ts" {
			t.Errorf("output = %q, want 'ERROR in file.ts'", output)
		}
		if !hasErrors {
			t.Errorf("hasErrors = false, want true")
		}
	})
}

func TestCheck_BlocksUntilServerResponds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		server := newFakeCheckServer(serverConn)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.serve(ctx)

		client := createTestClient(clientConn)

		// Start the request - it should block since no response is queued yet
		type result struct {
			output    string
			hasErrors bool
			err       error
		}
		resultCh := make(chan result, 1)
		go func() {
			output, hasErrors, err := client.Check(ctx, "human")
			resultCh <- result{output, hasErrors, err}
		}()

		// Wait a bit to ensure the request is blocking
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		// Now queue the response - should unblock the request
		server.responses <- checkResponse{output: "check complete", hasErrors: false}
		synctest.Wait()

		select {
		case r := <-resultCh:
			if r.err != nil {
				t.Fatalf("Check returned error: %v", r.err)
			}
			if r.output != "check complete" {
				t.Errorf("output = %q, want 'check complete'", r.output)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Check did not return after response was queued")
		}
	})
}

func TestCheck_RespectsContextCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() { _ = serverConn.Close() }()
		defer func() { _ = clientConn.Close() }()

		server := newFakeCheckServer(serverConn)
		serverCtx, serverCancel := context.WithCancel(context.Background())
		defer serverCancel()

		// Server runs but never queues a response - simulates blocking
		go server.serve(serverCtx)

		client := createTestClient(clientConn)

		// Create a context that we'll cancel
		ctx, cancel := context.WithCancel(context.Background())

		// Start Check in a goroutine
		errCh := make(chan error, 1)
		go func() {
			_, _, err := client.Check(ctx, "human")
			errCh <- err
		}()

		// Wait a bit, then cancel
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		cancel()
		synctest.Wait()

		// Should return quickly with context error
		select {
		case err := <-errCh:
			// The error may be wrapped, so check if context was canceled
			if !strings.Contains(err.Error(), "context canceled") {
				t.Errorf("Expected context canceled error, got: %v", err)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Check did not return after context cancellation")
		}
	})
}
