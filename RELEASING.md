# Releasing scout

The release pipeline is [GoReleaser](https://goreleaser.com): it builds Linux/macOS/Windows binaries on amd64 + arm64, creates a GitHub Release on `tool7/scout`, and publishes a Homebrew cask to `tool7/homebrew-tap`. Configuration lives in `.goreleaser.yaml`.

## One-time setup

- [ ] **GoReleaser installed** — `brew install goreleaser`
- [ ] **GitHub token** at `~/.config/goreleaser/github_token` (`chmod 600`)
  - Fine-grained PAT with **Contents: Read and write** on both `tool7/scout` and `tool7/homebrew-tap`
- [ ] **`tool7/homebrew-tap` reachable** with at least one commit on `main`
- [ ] **Atlassian OAuth 2.0 (3LO) app registered** at [developer.atlassian.com](https://developer.atlassian.com/console/myapps/) — see [OAuth credentials](#oauth-credentials) below for the required scopes and callback URL. Save the resulting `client_id` and `client_secret`; they are injected into release builds via `-ldflags` and must be present in the shell environment as `SCOUT_JIRA_CLIENT_ID` / `SCOUT_JIRA_CLIENT_SECRET` whenever you run `goreleaser release`.
- [ ] **GitHub OAuth App registered** at [github.com/settings/developers](https://github.com/settings/developers) — see [GitHub OAuth credentials](#github-oauth-credentials) below. Save the resulting `client_id` (Device Flow needs no secret) and supply it as `SCOUT_GITHUB_CLIENT_ID` whenever you run `goreleaser release`.

## Cut a release

1. **Confirm `main` is clean and pushed.**

   ```sh
   git status
   git log origin/main..HEAD   # should be empty
   ```

2. **Pick the next version** following semver: `vMAJOR.MINOR.PATCH`.

3. **Export OAuth credentials.** They are read by `.goreleaser.yaml` and embedded in the binaries.

   ```sh
   export SCOUT_JIRA_CLIENT_ID="..."
   export SCOUT_JIRA_CLIENT_SECRET="..."
   export SCOUT_GITHUB_CLIENT_ID="..."
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
   scout github-login          # prints user code, opens github.com/login/device
   ```

## OAuth credentials

Scout authenticates to Jira via Atlassian OAuth 2.0 (3LO). Atlassian requires a `client_secret` even for installed/CLI apps (it does not support pure-public PKCE-only clients), so a single "Scout" OAuth app is registered once at developer.atlassian.com and its `client_id` + `client_secret` are baked into release binaries via `-ldflags -X`. End users **never** see those credentials and never need to register their own app — they just run `scout jira-login`.

### Required app configuration

- **Authorization type**: OAuth 2.0 (3LO)
- **Permissions / scopes** (the only ones to pick in the console):
  - `read:jira-work`
  - `read:jira-user`

  > Note: scout also requests `offline_access` at login time so Atlassian issues a refresh token. That is a request-time OAuth scope, **not** a permission you select on the app — it does not appear in the developer-console picker, and you do not need to add it there. Leave it as-is in the source code.
- **Callback URL**: `http://127.0.0.1:53127/callback` — Atlassian requires an **exact** match (no wildcards, no port-less host), so the CLI binds a fixed loopback port. The constant lives at `RedirectPort` in [internal/jiraauth/config.go](internal/jiraauth/config.go); if you ever change it there, update the developer-console entry in lockstep or every login will fail with `redirect_uri_mismatch`.

### How they're injected at build time

`.goreleaser.yaml` references the env vars in `builds[].ldflags`:

```yaml
ldflags:
  - -X scout/internal/jiraauth.ClientID={{.Env.SCOUT_JIRA_CLIENT_ID}}
  - -X scout/internal/jiraauth.ClientSecret={{.Env.SCOUT_JIRA_CLIENT_SECRET}}
```

A binary built **without** these vars set still compiles and works for offline query commands (`search`, `history`, `related`, `status`), but `scout jira-login` and any `scout sync` that touches Jira will fail with a clear error pointing here.

For ad-hoc local testing without GoReleaser:

```sh
go build -ldflags "\
  -X scout/internal/jiraauth.ClientID=$SCOUT_JIRA_CLIENT_ID \
  -X scout/internal/jiraauth.ClientSecret=$SCOUT_JIRA_CLIENT_SECRET \
  -X scout/internal/githubauth.ClientID=$SCOUT_GITHUB_CLIENT_ID" \
  -o scout ./cmd/scout
```

### Threat model

The "secret" is, by necessity, distributed inside a public binary. Same tradeoff every Atlassian-targeting CLI makes. Acceptable because:

- Stealing it does not grant access to any user's Jira data; an attacker would still need to phish a user through the real `auth.atlassian.com` consent screen, which displays the registered app name ("Scout").
- It can be rotated at developer.atlassian.com if abuse is observed; users only need to upgrade the binary, not re-register anything. After rotation, ship a new release ASAP — installed binaries will start failing token refresh once the old credential is revoked.

## GitHub OAuth credentials

Scout authenticates to GitHub via OAuth **Device Flow** (RFC 8628). A single "Scout" OAuth App is registered once at github.com/settings/developers and its `client_id` is baked into release binaries via `-ldflags -X`. Device Flow does **not** use a `client_secret`, so there is no secret to protect — only the `client_id`, which is intentionally public.

### Required app configuration

1. Go to **https://github.com/settings/developers** → **OAuth Apps** → **New OAuth App**.
2. Fill in:
   - **Application name**: `Scout`
   - **Homepage URL**: `https://github.com/tool7/scout`
   - **Authorization callback URL**: `http://localhost` (required field; Device Flow ignores it but GitHub still demands a non-empty URL).
   - **Enable Device Flow**: ✅ **check this box**. Without it, the device-code endpoint returns `device_flow_disabled`.
3. Click **Register application**. Copy the resulting **Client ID** (a hex string starting with `Iv1.` or similar).
4. **Do not generate a client secret.** Device Flow doesn't need one and creating it just invites accidental future leakage.

### How it's injected at build time

`.goreleaser.yaml` references the env var in `builds[].ldflags`:

```yaml
ldflags:
  - -X scout/internal/githubauth.ClientID={{.Env.SCOUT_GITHUB_CLIENT_ID}}
```

A binary built **without** `SCOUT_GITHUB_CLIENT_ID` set still compiles and works for everything except `scout github-login`, which fails fast with a clear error referencing the rebuild instructions.

### Threat model

The `client_id` is non-secret by OAuth design. No `client_secret` to worry about. The only operational risk is the OAuth App getting deleted or disabled — at which point every installed scout binary breaks until a new release is cut with a fresh `client_id`. Rotate by registering a new app, updating the `SCOUT_GITHUB_CLIENT_ID` env var, and shipping a new release ASAP.
