# Release Process

## Versioning

The project uses [semantic versioning](https://semver.org/) with a `v` prefix: `v{MAJOR}.{MINOR}.{PATCH}` (e.g. `v0.3.0`).

## Branch Model

- **`main`** — Active development. All PRs target `main`.
- **`release-{MAJOR}.{MINOR}`** — Maintenance branches for each minor version (e.g. `release-0.3`). Created when a minor version is ready to ship.

## Creating a New Minor Release

1. Create a `release-{MAJOR}.{MINOR}` branch from `main` and push it
2. The `manage-release-tags` workflow detects the new branch and automatically creates the `v{MAJOR}.{MINOR}.0` tag
3. The `release` workflow triggers, builds multi-arch images, and creates the GitHub release

## Patch Releases

Patch releases are **automated**:

1. Cherry-pick fixes to the `release-{MAJOR}.{MINOR}` branch (manually, or via `/cherry-pick release-X.Y` comment on a merged PR)
2. Every Tuesday at 08:00 UTC, the `manage-release-tags` workflow checks all release branches for new commits since the last tag
3. If new commits exist, it increments the patch version (e.g. `v0.3.0` → `v0.3.1`) and creates a new tag
4. The `release` workflow builds and publishes the patch release

To trigger a patch release immediately (without waiting for Tuesday), manually run the `manage-release-tags` workflow via GitHub Actions.

## What the Release Workflow Produces

For each tag, the `release` workflow:

1. **Builds multi-arch container images** (`linux/amd64`, `linux/arm64`) and pushes to GHCR:
   - `ghcr.io/functions-dev/func-operator:{VERSION}`
   - `ghcr.io/functions-dev/func-operator:{MAJOR}.{MINOR}`
   - `ghcr.io/functions-dev/func-operator:{MAJOR}`
   - `ghcr.io/functions-dev/func-operator:latest` (only for the newest version)

2. **Generates an install manifest** (`func-operator.yaml`) with CRDs, RBAC, and deployment (using image digest for reproducibility)

3. **Builds an OLM bundle image** for catalog distribution:
   - `ghcr.io/functions-dev/func-operator-bundle:{VERSION}`

4. **Creates a GitHub release** with auto-generated release notes and the install manifest attached

## Nightly Builds

The `nightly-build` workflow runs daily at 00:00 UTC and pushes images from `main` tagged as `:main`.

## Cherry-Pick Automation

The `cherry-pick` workflow supports automated backporting:
- Comment `/cherry-pick release-X.Y` on a merged PR
- A new PR with the cherry-picked changes is automatically created against the target branch

## Emergency Release

For urgent fixes outside the Tuesday schedule:
1. Merge the fix to the release branch
2. Either manually trigger `manage-release-tags` via GitHub Actions, or push a tag directly:
   ```bash
   git tag v{MAJOR}.{MINOR}.{PATCH}
   git push origin v{MAJOR}.{MINOR}.{PATCH}
   ```
3. The `release` workflow triggers automatically on tag push