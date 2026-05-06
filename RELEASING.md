# Releasing scout

The release pipeline is [GoReleaser](https://goreleaser.com): it builds Linux/macOS/Windows binaries on amd64 + arm64, creates a GitHub Release on `tool7/scout`, and publishes a Homebrew cask to `tool7/homebrew-tap`. Configuration lives in `.goreleaser.yaml`.

## One-time setup

- [ ] **GoReleaser installed** — `brew install goreleaser`
- [ ] **GitHub token** at `~/.config/goreleaser/github_token` (`chmod 600`)
  - Fine-grained PAT with **Contents: Read and write** on both `tool7/scout` and `tool7/homebrew-tap`
- [ ] **`tool7/homebrew-tap` reachable** with at least one commit on `main`
- [ ] **Atlassian OAuth 2.0 (3LO) app registered** at [developer.atlassian.com](https://developer.atlassian.com/console/myapps/) — see [OAuth credentials](#oauth-credentials) below for the required scopes and callback URL. Save the resulting `client_id` and `client_secret`; they are injected into release builds via `-ldflags` and must be present in the shell environment as `SCOUT_OAUTH_CLIENT_ID` / `SCOUT_OAUTH_CLIENT_SECRET` whenever you run `goreleaser release`.

## Cut a release

1. **Confirm `main` is clean and pushed.**

   ```sh
   git status
   git log origin/main..HEAD   # should be empty
   ```

2. **Pick the next version** following semver: `vMAJOR.MINOR.PATCH`.

3. **Export OAuth credentials.** They are read by `.goreleaser.yaml` and embedded in the binaries.

   ```sh
   export SCOUT_OAUTH_CLIENT_ID="..."
   export SCOUT_OAUTH_CLIENT_SECRET="..."
   ```

4. **Snapshot dry run.** Builds everything in `dist/` without publishing.

   ```sh
   goreleaser release --snapshot --clean --skip=publish
   ```

   Spot-check `dist/homebrew/Casks/scout.rb` and one binary's `--version` output.

5. **Tag and push.**

   ```sh
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

6. **Release.**

   ```sh
   goreleaser release --clean
   ```

7. **Smoke test.**

   ```sh
   brew update
   brew upgrade tool7/tap/scout
   scout --version             # expect: vX.Y.Z
   scout jira-login            # browser opens, consent screen names "Scout"
   ```

## OAuth credentials

Scout authenticates to Jira via Atlassian OAuth 2.0 (3LO). Atlassian requires a `client_secret` even for installed/CLI apps (it does not support pure-public PKCE-only clients), so a single "Scout" OAuth app is registered once at developer.atlassian.com and its `client_id` + `client_secret` are baked into release binaries via `-ldflags -X`. End users **never** see those credentials and never need to register their own app — they just run `scout jira-login`.

### Required app configuration

- **Authorization type**: OAuth 2.0 (3LO)
- **Permissions / scopes** (the only ones to pick in the console):
  - `read:jira-work`
  - `read:jira-user`

  > Note: scout also requests `offline_access` at login time so Atlassian issues a refresh token. That is a request-time OAuth scope, **not** a permission you select on the app — it does not appear in the developer-console picker, and you do not need to add it there. Leave it as-is in the source code.
- **Callback URL**: `http://127.0.0.1:53127/callback` — Atlassian requires an **exact** match (no wildcards, no port-less host), so the CLI binds a fixed loopback port. The constant lives at `RedirectPort` in [internal/oauth/config.go](internal/oauth/config.go); if you ever change it there, update the developer-console entry in lockstep or every login will fail with `redirect_uri_mismatch`.

### How they're injected at build time

`.goreleaser.yaml` references the env vars in `builds[].ldflags`:

```yaml
ldflags:
  - -X scout/internal/oauth.ClientID={{.Env.SCOUT_OAUTH_CLIENT_ID}}
  - -X scout/internal/oauth.ClientSecret={{.Env.SCOUT_OAUTH_CLIENT_SECRET}}
```

A binary built **without** these vars set still compiles and works for offline query commands (`search`, `history`, `related`, `status`), but `scout jira-login` and any `scout sync` that touches Jira will fail with a clear error pointing here.

For ad-hoc local testing without GoReleaser:

```sh
go build -ldflags "\
  -X scout/internal/oauth.ClientID=$SCOUT_OAUTH_CLIENT_ID \
  -X scout/internal/oauth.ClientSecret=$SCOUT_OAUTH_CLIENT_SECRET" \
  -o scout ./cmd/scout
```

### Threat model

The "secret" is, by necessity, distributed inside a public binary. Same tradeoff every Atlassian-targeting CLI makes. Acceptable because:

- Stealing it does not grant access to any user's Jira data; an attacker would still need to phish a user through the real `auth.atlassian.com` consent screen, which displays the registered app name ("Scout").
- It can be rotated at developer.atlassian.com if abuse is observed; users only need to upgrade the binary, not re-register anything. After rotation, ship a new release ASAP — installed binaries will start failing token refresh once the old credential is revoked.
