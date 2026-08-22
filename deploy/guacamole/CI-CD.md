# Gitea CI/CD for the SessionGuard Guacamole extension

The workflow `.gitea/workflows/release.yml` publishes three artifacts on every
push to `main`:

1. `git.send.nrw/sendnrw/<repo>:<version>` - SessionGuard Master
2. `git.send.nrw/sendnrw/<repo>-guacamole:<version>` - Guacamole with the
   SessionGuard broker extension preinstalled
3. `sessionguard-guacamole.jar` in the Gitea Generic Package Registry under
   `<repo>-guacamole-extension/<version>`

Both container images also receive the `latest` tag.

## Required secrets

The workflow reuses the existing secrets:

- `DOCKER_USERNAME`
- `DOCKER_PASSWORD`

The account needs write access to both the OCI/container registry and the
Gitea Package Registry. If these permissions should be separated, create
`PACKAGE_USERNAME` and `PACKAGE_TOKEN` secrets and use those in the package
upload step.

## Compose

Instead of the stock Guacamole image, use the CI-built image:

```yaml
services:
  guacamole:
    image: git.send.nrw/sendnrw/sessionguard-guacamole:${SESSIONGUARD_VERSION:-latest}
    environment:
      SESSIONGUARD_MASTER_URL: http://sessionguard-master:8080
      SESSIONGUARD_BROKER_API_KEY: ${SESSIONGUARD_BROKER_API_KEY}
      SESSIONGUARD_BROKER_TIMEOUT_MS: "2500"
```

Keep all existing Guacamole/PostgreSQL/header-auth environment variables and
Traefik labels unchanged.

## Versioning

`fetch-depth: 0` is important. The release version is determined with:

```sh
git describe --tags --always | sed 's/^v//'
```

A commit tagged `v0.4.0` therefore publishes `0.4.0`; later commits are named
like `0.4.0-1-g0123456` until the next tag.

The extension Dockerfile no longer hardcodes `sessionguard-guacamole-0.4.0.jar`.
Maven may therefore change the project version without requiring a Dockerfile
change.
