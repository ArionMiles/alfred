# Run the daemon with Docker Compose

The daemon image runs `alfred serve`. Conversations, saved credentials, and
provider settings persist in the `alfred-data` volume. The default workspace is
`/var/lib/alfred/workspace`, inside that same volume; tools run in the daemon's
filesystem.

## Start Alfred

From the checkout, start the daemon:

```sh
docker compose up -d
```

Compose uses `ghcr.io/arionmiles/alfred:tip`. To build locally before images are
published, run:

```sh
docker compose up -d --build
```

Set `ALFRED_IMAGE` to select a versioned image or a pinned `tip-<sha>` build.
Set `ALFRED_PORT` to change the host port from 8080. The published port is bound
to the host's localhost. The daemon has no authentication; keep it on a trusted
network.

The supplied `deploy/config.toml` is mounted read-only. Edit that file for server
settings and recreate the container after changing them. The image includes zsh,
Git, curl, and CA certificates. The daemon runs as UID 1000 and owns its workspace
in the data volume.

## Connect a terminal client

Run the bundled TUI inside the running container:

```sh
docker compose exec alfred alfred
```

The TUI discovers the existing daemon using their shared data directory and
opens the current workspace. Open `/providers` to save your OpenAI key, then
`/models` to select a model. Workspace files, keys, and selection persist across
container recreation. Creating and switching projects from the TUI are not yet
available.

Containers on the same Compose network can connect to `http://alfred:8080`.
A future web client can use that address without accessing Alfred's data volume.

## Update and stop

```sh
docker compose pull
docker compose up -d
```

For locally built images, use `docker compose up -d --build` instead.

```sh
docker compose logs -f alfred
docker compose down
```

Stopping the deployment retains the data volume. `docker compose down --volumes`
also deletes saved conversations, settings, and credentials.
