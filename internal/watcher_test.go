package internal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
)

// resetWatcherCount resets the global watcher count for testing.
func resetWatcherCount() {
	globalWatcherCount.Store(0)
}

// FakeFSWatcher implements FSWatcher for testing.
type FakeFSWatcher struct {
	events      chan fsnotify.Event
	errors      chan error
	addedPaths  []addedPath
	rescanCount int
}

type addedPath struct {
	path      string
	recursive bool
}

func NewFakeFSWatcher() *FakeFSWatcher {
	return &FakeFSWatcher{
		events: make(chan fsnotify.Event),
		errors: make(chan error),
	}
}

func (f *FakeFSWatcher) Events() <-chan fsnotify.Event { return f.events }
func (f *FakeFSWatcher) Errors() <-chan error          { return f.errors }

func (f *FakeFSWatcher) Add(path string, recursive bool) error {
	f.addedPaths = append(f.addedPaths, addedPath{path: path, recursive: recursive})
	return nil
}

func (f *FakeFSWatcher) Rescan() error {
	f.rescanCount++
	return nil
}

func (f *FakeFSWatcher) Close() error {
	close(f.events)
	return nil
}

// FakeGitBranchWatcher implements GitBranchWatcher for testing.
type FakeGitBranchWatcher struct {
	headCh   chan struct{}
	branchCh chan struct{}
}

func NewFakeGitBranchWatcher() *FakeGitBranchWatcher {
	return &FakeGitBranchWatcher{
		headCh:   make(chan struct{}),
		branchCh: make(chan struct{}),
	}
}

func (f *FakeGitBranchWatcher) HeadChanged() <-chan struct{}   { return f.headCh }
func (f *FakeGitBranchWatcher) BranchChanged() <-chan struct{} { return f.branchCh }
func (f *FakeGitBranchWatcher) Start(ctx context.Context) {
	<-ctx.Done()
}
func (f *FakeGitBranchWatcher) Close() error {
	return nil
}

func TestWatcher_HeadChange_TriggersRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		restartCalled := false
		callbacks := WatcherCallbacks{
			OnRestart:    func() { restartCalled = true },
			OnSvelteSync: func() {},
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Send HEAD change event
		gitWatcher.headCh <- struct{}{}
		synctest.Wait()

		// Callback should not be called yet (debounce)
		if restartCalled {
			t.Fatal("OnRestart called before debounce interval")
		}

		// Advance time past debounce interval (250ms)
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if !restartCalled {
			t.Fatal("OnRestart not called after debounce interval")
		}
	})
}

func TestWatcher_BranchChange_TriggersRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		restartCalled := false
		callbacks := WatcherCallbacks{
			OnRestart:    func() { restartCalled = true },
			OnSvelteSync: func() {},
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Send branch change event
		gitWatcher.branchCh <- struct{}{}
		synctest.Wait()

		// Advance time past debounce interval
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if !restartCalled {
			t.Fatal("OnRestart not called after branch change")
		}
	})
}

func TestWatcher_DebounceMultipleEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		restartCount := 0
		callbacks := WatcherCallbacks{
			OnRestart:    func() { restartCount++ },
			OnSvelteSync: func() {},
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Send multiple rapid events
		gitWatcher.headCh <- struct{}{}
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)

		gitWatcher.branchCh <- struct{}{}
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)

		gitWatcher.headCh <- struct{}{}
		synctest.Wait()

		// Advance time past debounce interval from last event
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		// Should only be called once due to debouncing
		if restartCount != 1 {
			t.Fatalf("OnRestart called %d times, want 1", restartCount)
		}
	})
}

func TestWatcher_RouteFileCreate_TriggersSvelteSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		syncCalled := false
		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() { syncCalled = true },
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Send route file create event
		fsWatcher.events <- fsnotify.Event{
			Name: "/fake/workspace/src/routes/+page.ts",
			Op:   fsnotify.Create,
		}
		synctest.Wait()

		// Advance time past debounce interval
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if !syncCalled {
			t.Fatal("OnSvelteSync not called after route file creation")
		}
	})
}

func TestWatcher_RouteFileVariants_TriggerSvelteSync(t *testing.T) {
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
			synctest.Test(t, func(t *testing.T) {
				fsWatcher := NewFakeFSWatcher()
				gitWatcher := NewFakeGitBranchWatcher()

				syncCalled := false
				callbacks := WatcherCallbacks{
					OnRestart:    func() {},
					OnSvelteSync: func() { syncCalled = true },
				}

				config := WatcherConfig{
					WorkspacePath: "/fake/workspace",
				}

				w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				go w.Start(ctx)
				synctest.Wait()

				fsWatcher.events <- fsnotify.Event{
					Name: "/fake/workspace/src/routes/" + filename,
					Op:   fsnotify.Create,
				}
				synctest.Wait()

				time.Sleep(300 * time.Millisecond)
				synctest.Wait()

				if !syncCalled {
					t.Fatalf("OnSvelteSync not called for %s", filename)
				}
			})
		})
	}
}

func TestWatcher_NonRouteFile_DoesNotTriggerSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		syncCalled := false
		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() { syncCalled = true },
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Send non-route file event
		fsWatcher.events <- fsnotify.Event{
			Name: "/fake/workspace/src/lib/utils.ts",
			Op:   fsnotify.Create,
		}
		synctest.Wait()

		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if syncCalled {
			t.Fatal("OnSvelteSync called for non-route file")
		}
	})
}

func TestWatcher_RouteFileModify_DoesNotTriggerSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		syncCalled := false
		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() { syncCalled = true },
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Modify (Write) should not trigger sync, only Create/Remove/Rename
		fsWatcher.events <- fsnotify.Event{
			Name: "/fake/workspace/src/routes/+page.ts",
			Op:   fsnotify.Write,
		}
		synctest.Wait()

		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if syncCalled {
			t.Fatal("OnSvelteSync called for route file modify (should only trigger on create/remove/rename)")
		}
	})
}

func TestWatcher_CreateEvent_TriggersRescan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() {},
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Send create event for a new directory
		fsWatcher.events <- fsnotify.Event{
			Name: "/fake/workspace/src/newdir",
			Op:   fsnotify.Create,
		}
		synctest.Wait()

		if fsWatcher.rescanCount != 1 {
			t.Fatalf("Rescan called %d times, want 1", fsWatcher.rescanCount)
		}
	})
}

func TestWatcher_ContextCancellation_Stops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() {},
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			w.Start(ctx)
			close(done)
		}()
		synctest.Wait()

		cancel()
		synctest.Wait()

		// Verify Start() returned (channel closed)
		select {
		case <-done:
			// Success - Start returned
		default:
			t.Fatal("Start did not return after context cancellation")
		}
	})
}

func TestWatcher_NilGitWatcher_Works(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()

		syncCalled := false
		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() { syncCalled = true },
		}

		config := WatcherConfig{
			WorkspacePath: "/fake/workspace",
		}

		// Pass nil for git watcher (not a git repo)
		w := NewWatcher(config, callbacks, fsWatcher, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Should still handle FS events
		fsWatcher.events <- fsnotify.Event{
			Name: "/fake/workspace/src/routes/+page.ts",
			Op:   fsnotify.Create,
		}
		synctest.Wait()

		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if !syncCalled {
			t.Fatal("OnSvelteSync not called when gitWatcher is nil")
		}
	})
}

func TestWatcher_RouteFilePattern_NegativeCases(t *testing.T) {
	// These files should NOT trigger svelte-kit sync
	// This tests the routeFilePattern regex edge cases
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
		// Files with route names in path but not as filename
		"page/utils.ts",
		"layout/helpers.js",
	}

	for _, filename := range nonRouteFiles {
		t.Run(filename, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fsWatcher := NewFakeFSWatcher()
				gitWatcher := NewFakeGitBranchWatcher()

				syncCalled := false
				callbacks := WatcherCallbacks{
					OnRestart:    func() {},
					OnSvelteSync: func() { syncCalled = true },
				}

				config := WatcherConfig{
					WorkspacePath: "/fake/workspace",
				}

				w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				go w.Start(ctx)
				synctest.Wait()

				fsWatcher.events <- fsnotify.Event{
					Name: "/fake/workspace/src/routes/" + filename,
					Op:   fsnotify.Create,
				}
				synctest.Wait()

				time.Sleep(300 * time.Millisecond)
				synctest.Wait()

				if syncCalled {
					t.Fatalf("OnSvelteSync should NOT be called for %s", filename)
				}
			})
		})
	}
}

func TestWatcher_AddsPaths(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fsWatcher := NewFakeFSWatcher()
		gitWatcher := NewFakeGitBranchWatcher()

		callbacks := WatcherCallbacks{
			OnRestart:    func() {},
			OnSvelteSync: func() {},
		}

		config := WatcherConfig{
			WorkspacePath:    "/fake/workspace",
			RecursiveDirs:    []string{"src", "lib"},
			NonRecursiveDirs: []string{"."},
		}

		w := NewWatcher(config, callbacks, fsWatcher, gitWatcher)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go w.Start(ctx)
		synctest.Wait()

		// Check that paths were added
		if len(fsWatcher.addedPaths) != 3 {
			t.Fatalf("Added %d paths, want 3", len(fsWatcher.addedPaths))
		}

		// Non-recursive first (filepath.Join normalizes "/fake/workspace" + "." to "/fake/workspace")
		if fsWatcher.addedPaths[0].path != "/fake/workspace" || fsWatcher.addedPaths[0].recursive {
			t.Errorf("First path: got %+v, want non-recursive '/fake/workspace'", fsWatcher.addedPaths[0])
		}

		// Then recursive
		if fsWatcher.addedPaths[1].path != "/fake/workspace/src" || !fsWatcher.addedPaths[1].recursive {
			t.Errorf("Second path: got %+v, want recursive 'src'", fsWatcher.addedPaths[1])
		}

		if fsWatcher.addedPaths[2].path != "/fake/workspace/lib" || !fsWatcher.addedPaths[2].recursive {
			t.Errorf("Third path: got %+v, want recursive 'lib'", fsWatcher.addedPaths[2])
		}
	})
}

func TestWatcherLimit_AcquireAndRelease(t *testing.T) {
	resetWatcherCount()
	defer resetWatcherCount()

	if err := acquireWatcher(); err != nil {
		t.Fatalf("acquireWatcher failed: %v", err)
	}

	if count := WatcherCount(); count != 1 {
		t.Fatalf("WatcherCount = %d, want 1", count)
	}

	releaseWatcher()

	if count := WatcherCount(); count != 0 {
		t.Fatalf("WatcherCount = %d, want 0", count)
	}
}

func TestWatcherLimit_ExceedsMax(t *testing.T) {
	resetWatcherCount()
	defer resetWatcherCount()

	globalWatcherCount.Store(MaxWatchers)

	err := acquireWatcher()
	if err == nil {
		t.Fatal("acquireWatcher should have failed when at MaxWatchers")
	}

	if !errors.Is(err, ErrTooManyWatchers) {
		t.Fatalf("expected ErrTooManyWatchers, got %v", err)
	}
}

func TestWatcherLimit_ConcurrentAcquire(t *testing.T) {
	resetWatcherCount()
	defer resetWatcherCount()

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()
			if err := acquireWatcher(); err != nil {
				t.Errorf("acquireWatcher failed: %v", err)
			}
		}()
	}

	wg.Wait()

	if count := WatcherCount(); count != numGoroutines {
		t.Fatalf("WatcherCount = %d, want %d", count, numGoroutines)
	}
}

func TestWatcherLimit_RealFSWatcher_IncrementsCount(t *testing.T) {
	resetWatcherCount()
	defer resetWatcherCount()

	if count := WatcherCount(); count != 0 {
		t.Fatalf("WatcherCount should start at 0, got %d", count)
	}

	w, err := NewRealFSWatcher()
	if err != nil {
		t.Fatalf("NewRealFSWatcher failed: %v", err)
	}

	if count := WatcherCount(); count != 1 {
		t.Fatalf("WatcherCount = %d after creating watcher, want 1", count)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if count := WatcherCount(); count != 0 {
		t.Fatalf("WatcherCount = %d after closing watcher, want 0", count)
	}
}
