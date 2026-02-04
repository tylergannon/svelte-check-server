Feature: Full Integration Test Suite
  A comprehensive end-to-end test of svelte-check-server using a fresh SvelteKit project

  This test suite creates a new SvelteKit project from scratch, initializes a git
  repository, and exercises all features of svelte-check-server including file
  watching, git integration, error detection, and recovery.

  # ==========================================================================
  # Setup
  # ==========================================================================

  Background:
    Given I am in the svelte-check-server repository root
    And I create a fresh SvelteKit project:
      """
      mkdir -p ./tmp
      PROJECT_NAME="test-$(date +%s)-$RANDOM"
      bunx sv create "./tmp/$PROJECT_NAME" --no-add-ons --no-install --template demo --types ts
      cd "./tmp/$PROJECT_NAME"
      bun install
      git init
      git add -A
      git commit -m "Initial commit"
      """
    And I set WORKSPACE to "./tmp/$PROJECT_NAME"
    And I open two terminal windows (one for server, one for commands)

  # ==========================================================================
  # Phase 1: Basic Server Lifecycle
  # ==========================================================================

  Scenario: 1.1 - Start the server
    When I run `go run ./ start -w $WORKSPACE` in the server terminal
    Then I should see "Server started" in the output
    And I should see "svelte-check started" as it begins the initial check
    And eventually I should see "svelte-check completed: 0 errors, 0 warnings"
    And a socket file should exist at /tmp/*-svelte-check.sock

  Scenario: 1.2 - Check returns results
    Given the server is running and has completed initial check
    When I run `go run ./ check -w $WORKSPACE`
    Then the command should complete in under 100ms
    And the exit code should be 0
    And stdout should contain "COMPLETED"

  Scenario: 1.3 - Stop the server
    Given the server is running
    When I run `go run ./ stop -w $WORKSPACE`
    Then the server terminal should show the server exiting
    And the socket file should be removed
    And all child processes should be terminated:
      """
      ps aux | grep -E "svelte-check|bun" | grep $WORKSPACE
      # Should return nothing
      """

  # ==========================================================================
  # Phase 2: Error Detection and Recovery
  # ==========================================================================

  Scenario: 2.1 - Introduce a type error
    Given the server is running with no errors
    When I create a file with a type error:
      """
      cat > $WORKSPACE/src/lib/broken.ts << 'EOF'
      const x: number = "this is not a number";
      export default x;
      EOF
      """
    Then within 5 seconds, the server should show "svelte-check completed: 1 errors"
    When I run `go run ./ check -w $WORKSPACE`
    Then the exit code should be 1
    And stdout should contain "ERROR"
    And stdout should contain "broken.ts"
    And stdout should contain "not assignable"

  Scenario: 2.2 - Fix the error
    Given there is a type error from the previous scenario
    When I fix the error:
      """
      cat > $WORKSPACE/src/lib/broken.ts << 'EOF'
      const x: number = 42;
      export default x;
      EOF
      """
    Then within 5 seconds, the server should show "svelte-check completed: 0 errors"
    When I run `go run ./ check -w $WORKSPACE`
    Then the exit code should be 0
    And stdout should NOT contain "ERROR"

  Scenario: 2.3 - Multiple errors in multiple files
    Given the server is running with no errors
    When I create multiple files with errors:
      """
      cat > $WORKSPACE/src/lib/error1.ts << 'EOF'
      const a: boolean = 123;
      export default a;
      EOF

      cat > $WORKSPACE/src/lib/error2.ts << 'EOF'
      const b: string = true;
      export default b;
      EOF

      cat > $WORKSPACE/src/lib/error3.ts << 'EOF'
      const c: number[] = "not an array";
      export default c;
      EOF
      """
    Then within 10 seconds, the server should show "svelte-check completed: 3 errors"
    When I run `go run ./ check -w $WORKSPACE`
    Then the exit code should be 1
    And stdout should contain "error1.ts"
    And stdout should contain "error2.ts"
    And stdout should contain "error3.ts"

  Scenario: 2.4 - Clean up errors
    When I remove the error files:
      """
      rm $WORKSPACE/src/lib/error1.ts $WORKSPACE/src/lib/error2.ts $WORKSPACE/src/lib/error3.ts $WORKSPACE/src/lib/broken.ts
      """
    Then within 5 seconds, the server should show "svelte-check completed: 0 errors"

  # ==========================================================================
  # Phase 3: Git Integration
  # ==========================================================================

  Scenario: 3.1 - Create a feature branch
    Given the server is running with no errors
    When I run in the workspace:
      """
      cd $WORKSPACE && git checkout -b feature-branch
      """
    Then within 2 seconds, the server should show "Git HEAD changed"
    And the server should show "svelte-check started" (indicating restart)

  Scenario: 3.2 - Make changes on the feature branch
    Given I am on the feature-branch
    When I create a new file and commit:
      """
      cd $WORKSPACE
      cat > src/lib/feature.ts << 'EOF'
      export const feature = "new feature";
      EOF
      git add src/lib/feature.ts
      git commit -m "Add feature"
      """
    Then within 2 seconds, the server should show "Branch ref updated"
    And the server should restart svelte-check

  Scenario: 3.3 - Switch back to main branch
    Given I am on feature-branch
    When I run in the workspace:
      """
      cd $WORKSPACE && git checkout main
      """
    Then within 2 seconds, the server should show "Git HEAD changed"
    And the server should restart svelte-check
    And after completion, `check` should return exit code 0
    # Note: src/lib/feature.ts no longer exists on main

  Scenario: 3.4 - Merge the feature branch
    Given I am on the main branch
    When I merge the feature branch:
      """
      cd $WORKSPACE && git merge feature-branch -m "Merge feature"
      """
    Then within 2 seconds, the server should show "Branch ref updated"
    And the server should restart svelte-check
    # Now src/lib/feature.ts exists on main

  Scenario: 3.5 - Introduce error, commit, then fix
    Given I am on the main branch
    When I introduce an error and commit:
      """
      cd $WORKSPACE
      cat > src/lib/will-break.ts << 'EOF'
      const broken: number = "oops";
      export default broken;
      EOF
      git add src/lib/will-break.ts
      git commit -m "Add broken code"
      """
    Then within 5 seconds, the server should show errors
    When I run `go run ./ check -w $WORKSPACE`
    Then the exit code should be 1
    When I fix and commit:
      """
      cd $WORKSPACE
      cat > src/lib/will-break.ts << 'EOF'
      const fixed: number = 42;
      export default fixed;
      EOF
      git add src/lib/will-break.ts
      git commit -m "Fix broken code"
      """
    Then within 5 seconds, the server should show "0 errors"
    When I run `go run ./ check -w $WORKSPACE`
    Then the exit code should be 0

  # ==========================================================================
  # Phase 4: Rapid Changes (Stress Test)
  # ==========================================================================

  Scenario: 4.1 - Rapid file edits
    Given the server is running with no errors
    When I rapidly edit a file 10 times:
      """
      for i in $(seq 1 10); do
        echo "export const iteration = $i;" > $WORKSPACE/src/lib/rapid.ts
        sleep 0.1
      done
      """
    Then the server should NOT crash
    And eventually the server should stabilize and show "0 errors"
    And `check` should return exit code 0

  Scenario: 4.2 - Rapid branch switches
    Given the server is running
    When I rapidly switch branches:
      """
      cd $WORKSPACE
      for i in $(seq 1 5); do
        git checkout -b "rapid-branch-$i"
        git checkout main
        git branch -D "rapid-branch-$i"
      done
      """
    Then the server should NOT crash
    And the server should handle the rapid git changes gracefully
    And eventually `check` should return exit code 0

  Scenario: 4.3 - Rapid git commits
    Given the server is running with no errors
    When I make many rapid commits:
      """
      cd $WORKSPACE
      for i in $(seq 1 5); do
        echo "// commit $i" >> src/lib/rapid-commits.ts
        git add src/lib/rapid-commits.ts
        git commit -m "Rapid commit $i"
        sleep 0.2
      done
      """
    Then the server should NOT crash or panic
    And after stabilization, `check` should return exit code 0

  # ==========================================================================
  # Phase 5: Edge Cases
  # ==========================================================================

  Scenario: 5.1 - Check while svelte-check is running
    Given the server is running
    When I trigger a restart and immediately run check:
      """
      cd $WORKSPACE && git commit --allow-empty -m "trigger" &
      go run ./ check -w $WORKSPACE
      """
    Then the check command should wait for completion
    And return valid results (not stale or empty)

  Scenario: 5.2 - Server handles Svelte component errors
    Given the server is running with no errors
    When I introduce a Svelte component error:
      """
      cat > $WORKSPACE/src/lib/BadComponent.svelte << 'EOF'
      <script lang="ts">
        let count: number = "not a number";
      </script>

      <p>{count}</p>
      EOF
      """
    Then within 5 seconds, the server should detect the error
    When I run `go run ./ check -w $WORKSPACE`
    Then the exit code should be 1
    And stdout should contain "BadComponent.svelte"

  Scenario: 5.3 - Delete a file that had errors
    Given there is an error in BadComponent.svelte
    When I delete the file:
      """
      rm $WORKSPACE/src/lib/BadComponent.svelte
      """
    Then within 5 seconds, the server should show "0 errors"
    And `check` should return exit code 0

  Scenario: 5.4 - Stop server while check is in progress
    Given the server is running
    When I trigger a check and stop simultaneously:
      """
      go run ./ check -w $WORKSPACE &
      CHECK_PID=$!
      sleep 0.5
      go run ./ stop -w $WORKSPACE
      wait $CHECK_PID
      """
    Then neither command should hang indefinitely
    And no zombie processes should remain

  # ==========================================================================
  # Phase 6: Cleanup
  # ==========================================================================

  Scenario: 6.1 - Final cleanup
    When I stop the server if running:
      """
      go run ./ stop -w $WORKSPACE 2>/dev/null || true
      """
    And I verify no processes remain:
      """
      ps aux | grep -E "svelte-check|bun" | grep "$WORKSPACE" | grep -v grep
      # Should return nothing
      """
    And I verify no socket remains:
      """
      ls /tmp/*svelte-check.sock 2>/dev/null | grep "$PROJECT_NAME" || echo "clean"
      # Should echo "clean"
      """
    Then the test environment is clean

  Scenario: 6.2 - Remove test project
    When I remove the test project:
      """
      rm -rf $WORKSPACE
      """
    Then the test is complete

  # ==========================================================================
  # Running This Test Suite
  # ==========================================================================
  #
  # This is a manual integration test. Run it as follows:
  #
  # 1. Create the test project:
  #    mkdir -p ./tmp
  #    PROJECT_NAME="test-$(date +%s)-$RANDOM"
  #    bunx sv create "./tmp/$PROJECT_NAME" --no-add-ons --no-install --template demo --types ts
  #    cd "./tmp/$PROJECT_NAME" && bun install && git init && git add -A && git commit -m "Initial"
  #    cd ../..
  #    export WORKSPACE="./tmp/$PROJECT_NAME"
  #
  # 2. Start server in terminal 1:
  #    go run ./ start -w $WORKSPACE
  #
  # 3. Run test commands in terminal 2:
  #    go run ./ check -w $WORKSPACE
  #    # ... follow scenarios above ...
  #
  # 4. Cleanup:
  #    go run ./ stop -w $WORKSPACE
  #    rm -rf ./tmp
  #
  # Expected total time: 5-10 minutes for full manual walkthrough
  #
