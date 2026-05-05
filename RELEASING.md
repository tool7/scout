# Releasing scout

The release pipeline is [GoReleaser](https://goreleaser.com): it builds Linux/macOS/Windows binaries on amd64 + arm64, creates a GitHub Release on `tool7/scout`, and publishes a Homebrew cask to `tool7/homebrew-tap`. Configuration lives in `.goreleaser.yaml`.

## One-time setup

- [ ] **GoReleaser installed** — `brew install goreleaser`
- [ ] **GitHub token** at `~/.config/goreleaser/github_token` (`chmod 600`)
  - Fine-grained PAT with **Contents: Read and write** on both `tool7/scout` and `tool7/homebrew-tap`
- [ ] **`tool7/homebrew-tap` reachable** with at least one commit on `main`

## Cut a release

1. **Confirm `main` is clean and pushed.**

   ```sh
   git status
   git log origin/main..HEAD   # should be empty
   ```

2. **Pick the next version** following semver: `vMAJOR.MINOR.PATCH`.

3. **Snapshot dry run.** Builds everything in `dist/` without publishing.

   ```sh
   goreleaser release --snapshot --clean --skip=publish
   ```

   Spot-check `dist/homebrew/Casks/scout.rb` and one binary's `--version` output.

4. **Tag and push.**

   ```sh
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

5. **Release.**

   ```sh
   goreleaser release --clean
   ```

6. **Smoke test.**

   ```sh
   brew update
   brew upgrade tool7/tap/scout
   scout --version             # expect: vX.Y.Z
   ```
