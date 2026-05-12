# AGENTS.md

## Always Keep in Mind

Act like a professional software developer and engineer. Adhere to architecture, naming conventions and coding standards in this codebase. If unsure, read similar files and get inspiration from the rest of the codebase. If introducing new features, make sure to cover them via unit tests and don't forget to take edge cases into account.

## Project Overview

Kubernetes operator (controller-runtime) for managing middleware updates for serverless functions deployed with the `func` CLI. Written in Go, uses Ginkgo/Gomega for tests.

## Testing Strategy

Before committing, test locally following the table below:

| If changed | Target | Description |
|------------|--------|-------------|
| `*.go` files | `make test` | Unit tests |
| Any files | `make lint` | Linting |
| `api/` types | `make manifests generate` | Regenerate CRDs and DeepCopy |
| Significant changes | `make test-e2e` | E2E tests (Kind cluster with Gitea required) |

## Project Structure

- `api/v1alpha1/` - Function CRD types
- `internal/` - controller and business logic
- `test/e2e/` - e2e tests using in-cluster Gitea
- `test/utils/` - e2e test helpers (RepositoryProvider, func CLI wrappers)
- `hack/` - cluster setup scripts
- `config/` - Kustomize manifests

## Boundaries

### Always Do

- Run `make test` before considering any change complete
- Run `make lint` before commits
- Run `make manifests generate` after modifying `api/` types
- Read `CONTRIBUTING.md` for development setup and workflow

### Ask First

- Security-related code changes (authentication, credentials, secrets handling)
- API changes
- Adding new dependencies
- Modifying CI/GitHub Actions workflows

### Never Do

- Commit secrets, API keys, or credentials
- Delete files without explicit user approval
- Force push to main/master branch
- Skip tests or linting

## Important Documentation

Read these files to understand the project setup, conventions, and development workflow:

- `README.md` - user-facing usage, API reference table, installation
- `CONTRIBUTING.md` - development setup, workflow, coding conventions
- `docs/architecture.md` - system architecture, reconciliation flow, component interactions
- `docs/release.md` - release process, branching model, versioning
- `docs/development/` - developer guides (e.g. Gitea integration, test patterns)
- `docs/plans/` - design documents capturing rationale and tradeoffs for past decisions

After implementing a feature or making significant changes, check whether these docs need updating. The API reference table in README.md must stay in sync with `api/v1alpha1/function_types.go`.