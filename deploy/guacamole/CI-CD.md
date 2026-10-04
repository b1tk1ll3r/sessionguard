# CI/CD

SessionGuard ships GitHub Actions workflows in `.github/workflows/`. The previous Gitea workflows in `.gitea/workflows/` (registry `git.send.nrw`) remain for existing Gitea mirrors; GitHub ignores that directory.

## Workflows

| Workflow | Trigger | Does |
|---|---|---|
| `ci.yml` | pull requests, pushes to branches other than `main`, called by the release workflows | gofmt, `go vet`, Windows cross-build, `go test` on Linux **and** Windows (junction tests), Maven build of the extension, JavaScript syntax check (`scripts/check-embedded-js.mjs`) |
| `release.yml` | push to `main`, tags `v*`, manual | runs CI, then builds and pushes the three images to GHCR and builds the extension JAR (workflow artifact; on `v*` tags also attached to the GitHub Release) |
| `windows-agent-release.yml` | manual (`version`, `prerelease`) | runs CI, builds `sessionguard-agent.exe`, creates or extends GitHub Release `v<version>` with exe, zip (incl. `install-agent.ps1`, example config) and SHA256 sums |

Dependabot (`.github/dependabot.yml`) keeps actions, Go modules, Maven and base images current; Guacamole versions are intentionally excluded and bumped together with the deployed Guacamole release.

## Images

| Image | Content |
|---|---|
| `ghcr.io/<owner>/sessionguard` | SessionGuard Master |
| `ghcr.io/<owner>/sessionguard-edgeguard` | EdgeGuard |
| `ghcr.io/<owner>/sessionguard-guacamole` | Guacamole 1.6.0 + SessionGuard extension |

Tags:

- push to `main`: `latest`, `<git describe>` (e.g. `0.6.0-3-g0123456`), `sha-<short>`
- tag `v0.6.0`: `0.6.0`, `0.6`, `sha-<short>` (no `latest`)

Each image carries build provenance (SLSA, `mode=max`) and an SBOM in GHCR.

## Secrets and permissions

No custom secrets are required: the workflows authenticate with the built-in `GITHUB_TOKEN` (`packages: write` for GHCR, `contents: write` only for the release steps).

Under *Settings → Actions → General → Workflow permissions*, "Read repository contents and packages permissions" is sufficient, because every job requests its permissions explicitly.

## Pulling the images

GHCR packages of a new repository are **private** by default. Either make them public (*Package → Package settings → Change visibility*) or log in on the Docker hosts with a personal access token (classic) that has `read:packages`:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

Then point the stacks at GHCR, e.g.:

```dotenv
# production/sessionguard/.env
SESSIONGUARD_IMAGE=ghcr.io/<owner>/sessionguard:latest
# production/guac-worker/.env.guacXX
SESSIONGUARD_GUAC_IMAGE=ghcr.io/<owner>/sessionguard-guacamole:latest
# production/public-vps/.env
EDGEGUARD_IMAGE=ghcr.io/<owner>/sessionguard-edgeguard
```

For production, prefer a fixed version tag (`:0.6.0`) over `latest`.

## Versioning

`fetch-depth: 0` is required, because the version is determined with:

```sh
git describe --tags --always | sed 's/^v//'
```

Recommended release flow:

1. `git tag v0.6.0 && git push origin v0.6.0`: images `:0.6.0` and a GitHub Release with the extension JAR.
2. Run *Actions → windows-agent-release* with `version = 0.6.0`: the agent assets are added to the same release.

A tag created by `windows-agent-release` itself does **not** trigger `release.yml` (GitHub suppresses workflow runs caused by `GITHUB_TOKEN`), which is why the tag push comes first.
