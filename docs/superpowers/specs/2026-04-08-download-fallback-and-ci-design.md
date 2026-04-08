# Download Fallback and Cross-Platform CI

**Date:** 2026-04-08
**Issue:** [ory/lumen#110](https://github.com/ory/lumen/issues/110)
**Status:** Draft

## Problem

The plugin startup scripts (`scripts/run.sh`, `scripts/run.bat`) read the
version from `.release-please-manifest.json` and construct a GitHub release
download URL. If that version has not been released yet (release-please bumps the
manifest before the release workflow completes, or the release job fails), the
download 404s and the plugin fails to start with no recovery path.

There is also no CI coverage verifying that the download scripts actually work
end-to-end on any platform.

## Goals

1. Make the download scripts resilient to manifest/release version mismatches by
   falling back to the latest published release.
2. Add a `version` subcommand so CI (and users) can verify the downloaded binary
   is functional.
3. Add cross-platform CI tests (Linux, macOS, Windows) that exercise the real
   download path against published GitHub releases.

## Non-Goals

- Changing the release-please workflow or manifest management.
- Caching downloaded binaries across CI runs.
- Supporting private repositories or authenticated downloads in the scripts.

## Design

### 1. Fallback in `scripts/run.sh`

The current download block (lines 33-57) attempts a single `curl -fL` for the
manifest-pinned version. The change wraps this in a fallback:

```bash
# 1. Read VERSION from .release-please-manifest.json (existing logic)
# 2. Attempt download
if ! curl -fL --progress-bar --max-time 300 --retry 3 --retry-delay 2 "$URL" -o "$BINARY"; then
  # 3. Fallback: resolve latest release via GitHub API
  echo "Version $VERSION not found, resolving latest release..." >&2

  # Use GITHUB_TOKEN for auth when available (CI), fall back to unauthenticated
  AUTH_ARGS=()
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    AUTH_ARGS=(-H "Authorization: token $GITHUB_TOKEN")
  fi

  LATEST_TAG=$(curl -sfL "${AUTH_ARGS[@]}" \
    --max-time 30 --retry 2 --retry-delay 2 \
    "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

  # Validate: tag must look like a version (vN.N.N...)
  if [ -z "$LATEST_TAG" ] || ! echo "$LATEST_TAG" | grep -qE '^v[0-9]'; then
    echo "Error: could not resolve latest release from GitHub API" >&2
    exit 1
  fi

  echo "Falling back to ${LATEST_TAG}..." >&2
  VERSION="$LATEST_TAG"
  ASSET="lumen-${VERSION#v}-${OS}-${ARCH}"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

  curl -fL --progress-bar --max-time 300 --retry 3 --retry-delay 2 "$URL" -o "$BINARY"
fi
chmod +x "$BINARY"
```

The fallback only activates on download failure, preserving the current
deterministic manifest-first behavior when releases are healthy. When
`GITHUB_TOKEN` is set (as in CI), the API call uses it for authentication,
avoiding unauthenticated rate limits. On end-user machines without the token,
the 60 req/hr unauthenticated limit is sufficient since this is a one-time
fallback.

### 2. Fallback in `scripts/run.bat`

Same logic adapted for Windows batch scripting:

```bat
:: Attempt primary download
curl -sfL --max-time 300 --retry 3 --retry-delay 2 "!URL!" -o "%BINARY%"
if errorlevel 1 (
  echo Version !VERSION! not found, resolving latest release... >&2

  :: Use GITHUB_TOKEN for auth when available
  set "AUTH_HEADER="
  if defined GITHUB_TOKEN set "AUTH_HEADER=-H "Authorization: token %GITHUB_TOKEN%""

  :: Query GitHub API for latest release tag
  set "TMPJSON=%TEMP%\lumen-latest.json"
  curl -sfL %AUTH_HEADER% --max-time 30 --retry 2 --retry-delay 2 ^
    "https://api.github.com/repos/!REPO!/releases/latest" -o "!TMPJSON!"

  :: Extract tag_name using findstr + for /f
  set "LATEST_TAG="
  for /f "tokens=2 delims=:" %%a in ('findstr /r "tag_name" "!TMPJSON!"') do (
    set "LATEST_TAG=%%~a"
    set "LATEST_TAG=!LATEST_TAG: =!"
    set "LATEST_TAG=!LATEST_TAG:,=!"
    set "LATEST_TAG=!LATEST_TAG:"=!"
  )
  del "!TMPJSON!" 2>nul

  :: Validate: tag must be non-empty and start with v followed by a digit
  if "!LATEST_TAG!"=="" (
    echo Error: could not resolve latest release from GitHub API >&2
    exit /b 1
  )
  echo !LATEST_TAG! | findstr /r "^v[0-9]" >nul 2>&1
  if errorlevel 1 (
    echo Error: resolved tag "!LATEST_TAG!" does not look like a version >&2
    exit /b 1
  )

  echo Falling back to !LATEST_TAG!... >&2
  set "VERSION=!LATEST_TAG!"
  set "ASSET=lumen-!VERSION:~1!-windows-!ARCH!.exe"
  set "URL=https://github.com/!REPO!/releases/download/!VERSION!/!ASSET!"

  curl -sfL --max-time 300 --retry 3 --retry-delay 2 "!URL!" -o "%BINARY%"
  if errorlevel 1 (
    echo Error: fallback download also failed >&2
    exit /b 1
  )
)
```

### 3. `cmd/version.go` — Version Subcommand

New file with a simple `version` subcommand:

```go
package cmd

var buildVersion = "dev"

func init() {
    rootCmd.AddCommand(&cobra.Command{
        Use:   "version",
        Short: "Print the lumen version",
        Run: func(cmd *cobra.Command, args []string) {
            fmt.Println(buildVersion)
        },
    })
}
```

The `buildVersion` variable defaults to `"dev"` and is overridden at build time
via ldflags.

### 4. `.goreleaser.yml` — Ldflags Update

Add version injection to all four build targets:

```yaml
ldflags:
  - -s -w -X github.com/ory/lumen/cmd.buildVersion={{.Version}}
```

This ensures released binaries report their actual version.

### 5. `scripts/test_run.sh` — Offline Fallback Tests

Add test cases to the existing test script:

- **Fallback URL construction**: Given a resolved latest tag, verify the
  constructed download URL matches the expected pattern.
- **Version parsing from API response**: Given a mock JSON string containing
  `"tag_name": "v1.2.3"`, verify `sed` extracts `v1.2.3`.

These tests remain fully offline (no HTTP calls).

### 6. `.github/workflows/ci.yml` — Download Job

New `download` job added to the existing CI workflow:

```yaml
download:
  name: Download (${{ matrix.os }})
  runs-on: ${{ matrix.os }}
  if: github.actor != 'release-please[bot]'
  timeout-minutes: 10
  strategy:
    matrix:
      os: [ubuntu-latest, macos-latest, windows-latest]
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  steps:
    - uses: actions/checkout@v4

    # Happy path: set manifest to real latest release, download, verify
    - name: Get latest release tag
      id: latest
      run: |
        TAG=$(curl -sfL -H "Authorization: token $GITHUB_TOKEN" \
          https://api.github.com/repos/ory/lumen/releases/latest \
          | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
        echo "tag=$TAG" >> "$GITHUB_OUTPUT"
        echo "version=${TAG#v}" >> "$GITHUB_OUTPUT"
      shell: bash

    - name: Set manifest to latest release
      run: printf '{\n  ".": "%s"\n}\n' "${{ steps.latest.outputs.version }}" > .release-please-manifest.json
      shell: bash

    - name: Download via run script (happy path)
      id: happy_unix
      run: |
        OUTPUT=$(scripts/run.sh version)
        echo "version=$OUTPUT" >> "$GITHUB_OUTPUT"
      shell: bash
      if: runner.os != 'Windows'

    - name: Download via run.bat (happy path)
      id: happy_win
      run: |
        for /f "delims=" %%i in ('scripts\run.bat version') do set "VER=%%i"
        echo version=%VER%>> %GITHUB_OUTPUT%
      shell: cmd
      if: runner.os == 'Windows'

    - name: Verify version output (happy path)
      run: |
        VER="${{ steps.happy_unix.outputs.version }}${{ steps.happy_win.outputs.version }}"
        echo "Binary reported version: $VER"
        echo "$VER" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]' \
          || { echo "ERROR: version output does not match semver pattern"; exit 1; }
      shell: bash

    # Fallback path: set manifest to nonexistent version, verify fallback
    - name: Clear downloaded binary
      run: rm -rf bin/
      shell: bash

    - name: Set manifest to nonexistent version
      run: printf '{\n  ".": "99.99.99"\n}\n' > .release-please-manifest.json
      shell: bash

    - name: Download via run script (fallback path)
      id: fallback_unix
      run: |
        OUTPUT=$(scripts/run.sh version)
        echo "version=$OUTPUT" >> "$GITHUB_OUTPUT"
      shell: bash
      if: runner.os != 'Windows'

    - name: Download via run.bat (fallback path)
      id: fallback_win
      run: |
        for /f "delims=" %%i in ('scripts\run.bat version') do set "VER=%%i"
        echo version=%VER%>> %GITHUB_OUTPUT%
      shell: cmd
      if: runner.os == 'Windows'

    - name: Verify version output (fallback path)
      run: |
        VER="${{ steps.fallback_unix.outputs.version }}${{ steps.fallback_win.outputs.version }}"
        echo "Fallback binary reported version: $VER"
        echo "$VER" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]' \
          || { echo "ERROR: version output does not match semver pattern"; exit 1; }
      shell: bash
```

Key properties:
- Passes `GITHUB_TOKEN` as a job-level env var so both the scripts' fallback
  logic and the "Get latest release tag" step use authenticated API calls
  (5,000 req/hr limit instead of 60).
- Tests both the happy path (manifest matches a real release) and the fallback
  path (manifest points to a nonexistent version).
- Asserts that `lumen version` outputs a valid semver string (not `dev`).
- Uses `timeout-minutes: 10` to prevent hung downloads from burning CI minutes.
- The "Clear downloaded binary" step uses `shell: bash` which works on Windows
  via Git Bash (pre-installed on GHA Windows runners).
- Runs on all three platforms with `run.bat` for Windows and `run.sh` for
  Linux/macOS.

## File Changes Summary

| File | Change |
|------|--------|
| `scripts/run.sh` | Add fallback block after failed download |
| `scripts/run.bat` | Add fallback block after failed download |
| `scripts/test_run.sh` | Add offline tests for fallback logic |
| `cmd/version.go` | New file: `version` subcommand |
| `.goreleaser.yml` | Add ldflags for version injection |
| `.github/workflows/ci.yml` | New `download` job with 3-OS matrix |

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| GitHub API rate limit (60/hr unauthenticated) | Fallback only fires when manifest version is missing; scripts use `GITHUB_TOKEN` when set (CI always has it); end-user one-time fallback is well within 60/hr |
| Latest release binary is incompatible with shipped plugin code | Unlikely for patch versions; the alternative (hard crash) is worse. Log a warning so users know they got a fallback version |
| `sed`/`findstr` JSON parsing is fragile | The GitHub API response format for `tag_name` is stable; both scripts validate that extracted tag matches `v[0-9]*` pattern; offline tests cover parsing |
| Windows `findstr` parsing edge cases | Detailed pseudocode provided; CI tests the actual `run.bat` on Windows runners |
| API returns non-JSON (HTML error, CDN redirect) | Both scripts validate that parsed tag is non-empty and starts with `v[0-9]` before retrying; fall through to error exit if validation fails |
