package internal

import (
	"testing"
)

// TestParseGitHeadRef tests the parsing of .git/HEAD file content.
// This determines whether we're on a branch (ref: refs/heads/xxx) or
// in detached HEAD state (raw commit SHA).

func TestParseGitHeadRef_NormalBranch(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "main branch",
			content: "ref: refs/heads/main\n",
			want:    "refs/heads/main",
		},
		{
			name:    "master branch",
			content: "ref: refs/heads/master\n",
			want:    "refs/heads/master",
		},
		{
			name:    "feature branch with slashes",
			content: "ref: refs/heads/feature/my-feature\n",
			want:    "refs/heads/feature/my-feature",
		},
		{
			name:    "no trailing newline",
			content: "ref: refs/heads/main",
			want:    "refs/heads/main",
		},
		{
			name:    "with carriage return",
			content: "ref: refs/heads/main\r\n",
			want:    "refs/heads/main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitHeadRef(tt.content)
			if got != tt.want {
				t.Errorf("parseGitHeadRef(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestParseGitHeadRef_DetachedHead(t *testing.T) {
	// Detached HEAD shows a raw commit SHA, not a ref
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "full SHA",
			content: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2\n",
		},
		{
			name:    "another SHA",
			content: "1234567890abcdef1234567890abcdef12345678\n",
		},
		{
			name:    "SHA without newline",
			content: "abcdef1234567890abcdef1234567890abcdef12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitHeadRef(tt.content)
			if got != "" {
				t.Errorf("parseGitHeadRef(%q) = %q, want empty string for detached HEAD", tt.content, got)
			}
		})
	}
}

func TestParseGitHeadRef_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "empty content",
			content: "",
			want:    "",
		},
		{
			name:    "only whitespace",
			content: "   \n",
			want:    "",
		},
		{
			name:    "ref: with no path",
			content: "ref: \n",
			want:    "",
		},
		{
			name:    "partial ref prefix",
			content: "ref refs/heads/main\n",
			want:    "", // missing colon
		},
		{
			name:    "wrong prefix",
			content: "reference: refs/heads/main\n",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitHeadRef(tt.content)
			if got != tt.want {
				t.Errorf("parseGitHeadRef(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
