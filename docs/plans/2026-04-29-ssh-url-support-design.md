# SSH URL Support for Function CR

## Overview

Add support for SSH repository URLs in the Function CR, allowing users to specify repositories using SCP-style (`git@host:owner/repo.git`) or `ssh://` URLs. Currently, only HTTP/HTTPS URLs are supported.

## Design Decisions

- **URL-based auth detection**: The URL scheme determines the transport (SSH vs HTTP). No need for a secret to exist for public repos cloned over SSH.
- **Optional host key verification**: The auth secret can include a `known_hosts` field. If absent, host key checking is skipped (`InsecureIgnoreHostKey`).
- **go-git's `transport.ParseURL()`**: Replaces `net/url.Parse()` for URL parsing. Handles SCP-style URLs natively by normalizing them to `ssh://` scheme.
- **Random temp directories**: Simplify temp dir naming to fully random (`repo-*`) instead of deriving from the URL.

## Changes

### 1. Git Manager (`internal/git/manager.go`)

**`CloneRepository`:**
- Replace `neturl.Parse(repoUrl)` with `transport.ParseURL(repoUrl)` from `github.com/go-git/go-git/v6/plumbing/transport`.
- Simplify temp dir pattern to `os.MkdirTemp(cloneBaseDir, "repo-*")`.
- Pass the parsed URL to `getClientOptions` so it can branch on scheme.

**`getClientOptions`:**
- Signature changes to accept the parsed `*url.URL`.
- For `ssh` scheme:
  - If secret has `sshPrivateKey`: build `ssh.PublicKeys` via `ssh.NewPublicKeys("git", pemBytes, password)`.
  - If secret has `known_hosts`: set `HostKeyCallback` from the known_hosts data.
  - If no `known_hosts`: use `gossh.InsecureIgnoreHostKey()`.
  - If no secret: return `WithSSHAuth` using `InsecureIgnoreHostKey` only (public repo).
  - Wrap in `client.WithSSHAuth()`.
- For `http`/`https` scheme: existing token/username-password logic unchanged.

**Auth secret fields (SSH):**
- `sshPrivateKey` (required for private repos) — PEM-encoded private key
- `sshPrivateKeyPassword` (optional) — passphrase for encrypted keys
- `known_hosts` (optional) — known_hosts file content for host key verification

### 2. Unit Tests (`internal/git/manager_test.go`)

New test file covering `getClientOptions`:
- Empty secret + SSH URL → nil auth with InsecureIgnoreHostKey
- `sshPrivateKey` + SSH URL → WithSSHAuth with PublicKeys
- `sshPrivateKey` + `known_hosts` + SSH URL → SSH auth with known_hosts callback
- `sshPrivateKey` + `sshPrivateKeyPassword` + SSH URL → decrypts key
- Existing HTTP cases still work (token, username/password, empty)
- URL parsing: SCP-style, `ssh://`, `http://`, `https://` all resolve correctly

### 3. E2E Test Utilities

**`test/utils/gitea.go`:**
- `GetSSHEndpoint()` — reads `ssh` key from `gitea-endpoint` ConfigMap
- `CreateSSHKey(username, password, title, publicKey string)` — registers SSH public key via Gitea SDK `CreatePublicKey()`
- `SSHRepoURL(owner, repo string)` — builds SCP-style URL from SSH endpoint

**`test/utils/git.go`:**
- `WithSSHKey(privateKeyPath string)` option — configures `InitializeRepoWithFunction` to clone/push via SSH using `GIT_SSH_COMMAND` with the provided private key

### 4. E2E Tests (`test/e2e/func_deploy_test.go`)

Three new test cases under a new `Context("with an SSH repository URL", ...)`:

1. **Public repo with SSH URL** — Create public repo, push via HTTP, create Function CR with SSH URL, verify function becomes ready.
2. **Private repo with SSH key auth** — Generate SSH keypair, register public key in Gitea, create Secret with `sshPrivateKey`, create Function CR with SSH URL + authSecretRef, verify function becomes ready.
3. **Private repo without auth secret** — Create private repo, create Function CR with SSH URL but no authSecretRef, verify function fails with auth error.

### 5. README Updates

Add SSH examples to the existing "Git Authentication" section:
- Secret format with `sshPrivateKey`, `sshPrivateKeyPassword`, `known_hosts`
- Function CR example with SCP-style SSH URL + authSecretRef
- Function CR example with SSH URL for public repo (no secret)