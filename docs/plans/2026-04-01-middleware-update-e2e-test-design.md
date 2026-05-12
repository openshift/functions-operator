# E2E Test for Middleware Update Design

**Date:** 2026-04-01

## Overview

Add e2e test to verify that the operator updates middleware when it detects a function was deployed with an old version. The operator automatically detects version mismatch by comparing against the currently installed func CLI version on the operator pod.

## Implementation Plan

### 1. Create func CLI Wrapper Functions

**File:** `test/utils/func.go`

**Two functions:**

1. **RunFunc(args ...string) (string, error)**
   - Executes func CLI with current/latest version
   - Wraps `exec.Command("func", args...)`
   - Uses existing `Run()` helper for consistent output handling
   - Returns combined stdout/stderr and error

2. **RunFuncWithVersion(version string, args ...string) (string, error)**
   - Executes func CLI with a specific version
   - Downloads and caches the specified version if not already cached
   - Same return signature for consistency

**Version caching:**
- Cache location: `<project-root>/bin/func-cli/`
- Directory structure: `bin/func-cli/v1.20.0/func`, etc.
- Each version in its own subdirectory
- Already covered by `bin/` gitignore pattern

**Download logic:**
1. Get project directory using `GetProjectDir()`
2. Check if `<project-root>/bin/func-cli/<version>/func` exists
3. If exists → use it
4. If not exists:
   - Create `bin/func-cli/<version>/` directory
   - Download binary directly from: `https://github.com/knative/func/releases/download/<version>/func_<os>_<arch>`
   - Write to `bin/func-cli/<version>/func`
   - Make executable (`chmod +x`)
5. Execute with provided args

**Platform detection:** Use `runtime.GOOS` and `runtime.GOARCH`

**Error handling:**
- Clear errors for missing versions: "failed to download func v1.20.0: HTTP 404"
- Clear errors for download/write failures
- Preserve func CLI error output

### 2. Refactor Existing Tests (Separate Commit)

Replace all `exec.Command("func", ...)` calls with `utils.RunFunc(...)`:
- `func_deploy_test.go` line 66-71 (deploy command)
- `func_deploy_test.go` line 77 (delete command)  
- `git.go` line 42 in `InitializeRepoWithFunction` (init command)

### 3. Add New Middleware Update Test (Separate Commit)

**File:** `test/e2e/func_middleware_update_test.go`

**Test flow:**

1. **Setup:**
   - Create repository provider resources (user, repo)
   - Initialize repo with function code using `InitializeRepoWithFunction`
   - Create test namespace

2. **Deploy with old func CLI:**
   - Use `RunFuncWithVersion("v1.20.0", "deploy", "--namespace", ns, "--path", repoDir, ...)`
   - Creates initial deployment with old middleware
   - Commit func.yaml changes to git

3. **Create Function CR:**
   - Create Function resource pointing to repository
   - Triggers operator reconciliation

4. **Verify update:**
   - Eventually verify Function CR becomes Ready
   - This confirms operator successfully updated the middleware

**Old version:** Use `v1.20.0` as the hardcoded "old" version

## Dependencies

**New imports needed:**
- `io`
- `net/http`
- `runtime` (for OS/arch detection)

**No external dependencies required.**

## Testing

- Download logic tested automatically on first run of middleware update test
- Subsequent runs use cached binary
- Manual cleanup for testing: `rm -rf bin/func-cli/`

## Commit Strategy

1. **First commit:** Create wrapper functions and refactor existing tests to use RunFunc
2. **Second commit:** Add new middleware update e2e test
