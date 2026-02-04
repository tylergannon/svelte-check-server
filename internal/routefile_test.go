package internal

import (
	"testing"
)

func TestIsRouteFile_PositiveCases(t *testing.T) {
	routeFiles := []string{
		"+page.ts",
		"+page.js",
		"+layout.ts",
		"+layout.js",
		"+server.ts",
		"+server.js",
		"+page.server.ts",
		"+page.server.js",
		"+layout.server.ts",
		"+layout.server.js",
	}

	for _, filename := range routeFiles {
		t.Run(filename, func(t *testing.T) {
			if !isRouteFile(filename) {
				t.Errorf("isRouteFile(%q) = false, want true", filename)
			}
		})
	}
}

func TestIsRouteFile_PositiveCasesWithPath(t *testing.T) {
	// Should work with full paths too (extracts basename)
	paths := []string{
		"/workspace/src/routes/+page.ts",
		"/workspace/src/routes/blog/[slug]/+page.server.ts",
		"./src/routes/+layout.js",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			if !isRouteFile(path) {
				t.Errorf("isRouteFile(%q) = false, want true", path)
			}
		})
	}
}

func TestIsRouteFile_NegativeCases(t *testing.T) {
	nonRouteFiles := []string{
		// Svelte files - sync is only for TS/JS route files
		"+page.svelte",
		"+layout.svelte",
		"+error.svelte",
		// Regular files that happen to have route-like names
		"page.ts",        // missing + prefix
		"layout.ts",      // missing + prefix
		"+page.tsx",      // wrong extension
		"+page.jsx",      // wrong extension
		"+page.mjs",      // wrong extension
		"+page.cjs",      // wrong extension
		"+page.d.ts",     // type definition
		"+pages.ts",      // wrong name (plural)
		"+pageserver.ts", // missing dot
		// Other SvelteKit files that don't need sync
		"+error.ts",
		"hooks.server.ts",
		"hooks.client.ts",
		"app.html",
		// Random files
		"utils.ts",
		"README.md",
		"package.json",
	}

	for _, filename := range nonRouteFiles {
		t.Run(filename, func(t *testing.T) {
			if isRouteFile(filename) {
				t.Errorf("isRouteFile(%q) = true, want false", filename)
			}
		})
	}
}
