Feature: svelte-check-server
  A persistent svelte-check daemon that caches results for fast feedback

  Background:
    Given I am in the test-app directory
    And bun dependencies are installed (run `bun install` if needed)
    And no svelte-check-server is currently running for this workspace

  # ==========================================================================
  # IMPORTANT: Understanding svelte-check --watch behavior
  # ==========================================================================
  #
  # svelte-check --watch handles normal file edits itself. The daemon does NOT
  # restart svelte-check for every file change. It only restarts for events
  # that the TypeScript language service cannot handle incrementally:
  #
  # NO RESTART (svelte-check --watch handles these):
  #   - Normal file edits (.svelte, .ts, .js)
  #   - Saving files
  #   - Most incremental changes
  #
  # RESTART NEEDED (daemon triggers these):
  #   - Git branch switches (files may have different contents)
  #   - Git commit/pull/merge/rebase (multiple files change at once)
  #   - File deletions (can crash svelte-check, see issue #2773)
  #   - Route file changes (+page.ts, etc.) may need svelte-kit sync
  #
  # Philosophy: Err on the side of correctness over performance.
  # A false restart costs seconds. Stale diagnostics waste developer time.
  #
  # ==========================================================================

  # --------------------------------------------------------------------------
  # Basic Server Lifecycle
  # --------------------------------------------------------------------------

  Scenario: Start the server
    When I run `svelte-check-server start`
    Then the command should exit successfully
    And I should see "Server started" in the output
    And a socket file should exist at /tmp/<path-slug>-svelte-check.sock

  Scenario: Stop the server
    Given the server is running
    When I run `svelte-check-server stop`
    Then the server should stop
    And I should see "Server stopped" in the output

  Scenario: Server cleans up on SIGINT
    Given the server is running
    When I send SIGINT (Ctrl+C) to the server process
    Then the socket file should be removed
    And the svelte-check process should be terminated
    And all child processes (bun, node) should be terminated

  # --------------------------------------------------------------------------
  # Check Command
  # --------------------------------------------------------------------------

  Scenario: Check returns cached results when server is running
    Given the server is running
    And svelte-check has completed at least once (wait for "Watching for file changes")
    When I run `svelte-check-server check`
    Then the command should complete in under 1 second
    And I should see the svelte-check output (errors/warnings or success message)

  Scenario: Check runs directly when no server is running
    # No error - just runs svelte-check once without --watch
    Given no server is running
    When I run `svelte-check-server check`
    Then I should see "Server not running, running svelte-check directly..."
    And svelte-check should run once (without --watch)
    And I should see the svelte-check output
    And the exit code should be 0 if no errors, 1 if errors

  Scenario: Check waits for completion if check is in progress
    Given the server is running
    And svelte-check is currently running (not yet complete)
    When I run `svelte-check-server check`
    Then the command should wait until svelte-check completes
    And then return the results

  # --------------------------------------------------------------------------
  # Core Use Case: Exit Codes Reflect Check State
  # --------------------------------------------------------------------------
  # This is the primary use case for CI integration: the exit code tells you
  # whether the codebase has type errors or not.
  #
  # `check` command semantics:
  #   Exit 0 = no errors (warnings OK)
  #   Exit 1 = has errors
  #   Output: machine-format svelte-check output
  #
  # Output format (machine-readable svelte-check --output machine):
  #   1770255834342 ERROR "/path/to/file.ts" 10:5 "Type 'string' is not assignable to type 'number'"
  #   1770255834342 WARNING "/path/to/file.svelte" 3:1 "Unused CSS selector"
  # --------------------------------------------------------------------------

  Scenario: Clean codebase returns zero exit code
    # The happy path: code compiles, CI passes
    Given the server is running
    And svelte-check has completed
    And the codebase has no type errors
    When I run `svelte-check-server check`
    Then the exit code should be 0
    And stdout should contain the machine-format output (timestamps, file paths)
    And stdout should NOT contain " ERROR "

  Scenario: Codebase with errors returns non-zero exit code
    # The failing path: code has type errors, CI should fail
    Given the server is running
    And svelte-check has completed
    And I introduce a type error (e.g., assign string to number variable)
    And svelte-check detects the change and re-runs
    When I run `svelte-check-server check`
    Then the exit code should be 1
    And stdout should contain " ERROR "
    And stdout should contain the file path and line number of the error
    # Cleanup: revert the type error

  Scenario: Warnings do not affect exit code
    # Warnings are informational, should not fail CI
    Given the server is running
    And svelte-check has completed
    And the codebase has warnings but no errors (e.g., unused CSS selector)
    When I run `svelte-check-server check`
    Then the exit code should be 0
    And stdout should contain " WARNING "

  Scenario: Fix error and state recovers within same server session
    # Critical: the server correctly tracks state changes over time
    # This tests the full edit-check-fix-check cycle without restarting
    Given the server is running
    And svelte-check has completed with no errors
    When I introduce a type error in test-app/src/lib/example.ts
    And I wait for svelte-check to re-run and complete
    And I run `svelte-check-server check`
    Then the exit code should be 1
    And stdout should contain " ERROR "
    When I fix the type error (revert the file)
    And I wait for svelte-check to re-run and complete
    And I run `svelte-check-server check`
    Then the exit code should be 0
    And stdout should NOT contain " ERROR "

  Scenario: Multiple errors are all reported
    # All errors should be visible, not just the first one
    Given the server is running
    And svelte-check has completed
    When I introduce 3 different type errors in separate files
    And I wait for svelte-check to re-run and complete
    And I run `svelte-check-server check`
    Then the exit code should be 1
    And stdout should contain 3 lines with " ERROR "
    And each error should show its file path and position
    # Cleanup: revert all errors

  # --------------------------------------------------------------------------
  # File Changes - svelte-check --watch handles these
  # --------------------------------------------------------------------------

  Scenario: Modifying a Svelte file does NOT restart the daemon
    # svelte-check --watch handles normal file edits itself
    Given the server is running
    And svelte-check has completed
    When I modify test-app/src/routes/+page.svelte
    Then svelte-check should detect the change and re-run (via its own --watch)
    And the daemon should NOT restart the process
    And the cached output should update with new results

  Scenario: Changes in node_modules are ignored
    Given the server is running
    And svelte-check has completed
    When I create a file test-app/node_modules/test-file.txt
    Then svelte-check should NOT re-run
    # Cleanup: delete the file after

  # --------------------------------------------------------------------------
  # Git Operations - These SHOULD trigger restart
  # --------------------------------------------------------------------------

  Scenario: Switching git branches triggers restart
    # Branch switches can completely change file contents
    Given the server is running
    And svelte-check has completed
    And I am on the main branch
    When I run `git checkout -b test-branch`
    Then within 1 second, I should see "Git HEAD changed" in the server logs
    And svelte-check should be restarted (not just re-run via --watch)
    # Cleanup: git checkout main && git branch -D test-branch

  Scenario: Making a commit triggers restart
    # Commits indicate significant changes (especially after merge/rebase)
    Given the server is running
    And svelte-check has completed
    When I make a commit (e.g., `git commit --allow-empty -m "test"`)
    Then within 1 second, I should see "Branch ref updated" in the server logs
    And svelte-check should be restarted
    # Cleanup: git reset --hard HEAD~1

  Scenario: Git pull triggers restart
    # Pull can bring in changes to many files
    Given the server is running
    And there are changes to pull from remote
    When I run `git pull`
    Then svelte-check should be restarted

  Scenario: Git merge triggers restart
    Given the server is running
    When I merge another branch
    Then svelte-check should be restarted

  # --------------------------------------------------------------------------
  # Error Handling
  # --------------------------------------------------------------------------

  Scenario: Server handles svelte-check errors gracefully
    Given the server is running
    When I introduce a TypeScript error in a Svelte file
    And svelte-check completes
    Then `svelte-check-server check` should show the error
    And the server should continue running (not crash)
    # Cleanup: revert the error

  Scenario: Starting server when one is already running
    Given the server is running
    When I try to run `svelte-check-server start` again
    Then I should see an error about the socket already existing
    # Or it should detect the existing server and report it

  # --------------------------------------------------------------------------
  # Custom Watch Directories
  # --------------------------------------------------------------------------

  Scenario: Custom recursive watch directory
    When I run `svelte-check-server start -r ./src -r ./lib`
    Then the server should watch ./src recursively
    And the server should watch ./lib recursively

  Scenario: Custom non-recursive watch directory  
    When I run `svelte-check-server start -d .`
    Then the server should watch . non-recursively (only top-level files)

  # --------------------------------------------------------------------------
  # Future: File Deletions (not yet implemented)
  # --------------------------------------------------------------------------

  @future
  Scenario: File deletion triggers restart
    # svelte-check --watch can crash on file deletions (ENOENT)
    # See: https://github.com/sveltejs/language-tools/issues/2773
    Given the server is running
    And svelte-check has completed
    When I delete a watched file
    Then svelte-check should be restarted (to avoid crash)

  @future
  Scenario: Route file changes trigger svelte-kit sync
    # +page.ts, +layout.ts, etc. affect generated types
    Given the server is running
    When I create test-app/src/routes/new-route/+page.ts
    Then `svelte-kit sync` should be run
    And svelte-check should detect the new types

  # --------------------------------------------------------------------------
  # Manual Test Script
  # --------------------------------------------------------------------------
  
  # To run these tests manually:
  #
  # 1. Build the binary:
  #    go build -o svelte-check-server .
  #
  # 2. Setup:
  #    cd test-app && bun install && cd ..
  #
  # 3. Start server in one terminal:
  #    ./svelte-check-server start -w ./test-app
  #
  # 4. In another terminal, run checks:
  #    ./svelte-check-server check -w ./test-app
  #
  # 5. Test file watching (should NOT restart daemon):
  #    echo "<!-- test -->" >> test-app/src/routes/+page.svelte
  #    # svelte-check should re-run via its own --watch
  #    # Watch status for updated timestamp
  #    git checkout test-app/src/routes/+page.svelte  # revert
  #
  # 6. Test git watching (SHOULD restart daemon):
  #    git checkout -b test-branch
  #    # Watch for "Git HEAD changed" and restart
  #    git checkout main && git branch -D test-branch
  #
  # 7. Cleanup:
  #    ./svelte-check-server stop -w ./test-app
  #    # Or: Ctrl+C the server process
  #    # Verify socket is removed: ls /tmp/*svelte-check.sock
  #    # Verify all processes killed: ps aux | grep svelte-check
