package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSocketPathForWorkspace tests the path slugification algorithm.
// This function is critical for ensuring multiple projects don't collide
// on the same socket file, and that paths are deterministic.

func TestSocketPathForWorkspace_DifferentProjectsGetDifferentSockets(t *testing.T) {
	// Two different projects should get different socket paths
	path1, err := SocketPathForWorkspace("/home/user/project-a")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	path2, err := SocketPathForWorkspace("/home/user/project-b")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	if path1 == path2 {
		t.Errorf("Different projects should get different sockets: %q == %q", path1, path2)
	}
}

func TestSocketPathForWorkspace_SameProjectGetsSameSocket(t *testing.T) {
	// Same project should always get the same socket path (deterministic)
	path1, err := SocketPathForWorkspace("/home/user/myproject")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	path2, err := SocketPathForWorkspace("/home/user/myproject")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	if path1 != path2 {
		t.Errorf("Same project should get same socket: %q != %q", path1, path2)
	}
}

func TestSocketPathForWorkspace_NestedDirectoriesAreDistinct(t *testing.T) {
	// /home/user/project and /home/user/project/subdir should be different
	parent, err := SocketPathForWorkspace("/home/user/project")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	child, err := SocketPathForWorkspace("/home/user/project/subdir")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	if parent == child {
		t.Errorf("Parent and child directories should get different sockets: %q == %q", parent, child)
	}
}

func TestSocketPathForWorkspace_HasCorrectSuffix(t *testing.T) {
	path, err := SocketPathForWorkspace("/some/path")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	if !strings.HasSuffix(path, "-svelte-check.sock") {
		t.Errorf("Socket path should end with '-svelte-check.sock', got: %q", path)
	}
}

func TestSocketPathForWorkspace_IsInTmpDir(t *testing.T) {
	path, err := SocketPathForWorkspace("/some/path")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	tmpDir := os.TempDir()
	if !strings.HasPrefix(path, tmpDir) {
		t.Errorf("Socket path should be in temp dir %q, got: %q", tmpDir, path)
	}
}

func TestSocketPathForWorkspace_RelativePathIsResolved(t *testing.T) {
	// A relative path should be resolved to absolute
	// This is important because "." from different directories should yield different sockets

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	pathFromDot, err := SocketPathForWorkspace(".")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	pathFromAbs, err := SocketPathForWorkspace(cwd)
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	if pathFromDot != pathFromAbs {
		t.Errorf("Relative '.' and absolute cwd should resolve to same socket: %q != %q", pathFromDot, pathFromAbs)
	}
}

func TestSocketPathForWorkspace_TrailingSlashIsNormalized(t *testing.T) {
	// /home/user/project and /home/user/project/ should be the same
	withoutSlash, err := SocketPathForWorkspace("/home/user/project")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	withSlash, err := SocketPathForWorkspace("/home/user/project/")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	if withoutSlash != withSlash {
		t.Errorf("Trailing slash should be normalized: %q != %q", withoutSlash, withSlash)
	}
}

func TestSocketPathForWorkspace_SlugFormat(t *testing.T) {
	// Verify the slug format: slashes become dashes, leading slash is trimmed
	path, err := SocketPathForWorkspace("/home/user/project")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	filename := filepath.Base(path)
	expected := "home-user-project-svelte-check.sock"

	if filename != expected {
		t.Errorf("Slug format incorrect: got %q, want %q", filename, expected)
	}
}

func TestSocketPathForWorkspace_DeepNesting(t *testing.T) {
	// Deeply nested paths should still work and be distinct
	path, err := SocketPathForWorkspace("/a/b/c/d/e/f/g")
	if err != nil {
		t.Fatalf("SocketPathForWorkspace failed: %v", err)
	}

	filename := filepath.Base(path)
	expected := "a-b-c-d-e-f-g-svelte-check.sock"

	if filename != expected {
		t.Errorf("Deep nesting slug incorrect: got %q, want %q", filename, expected)
	}
}
