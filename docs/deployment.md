# Run the daemon with Docker Compose

The daemon image runs `alfred serve`. Conversations, saved credentials, and
provider settings persist in the `alfred-data` volume. The default workspace is
`/var/lib/alfred/workspace`, inside that same volume; tools run in the daemon's
filesystem.

## Start Alfred

In an empty directory, download the deployment files and start the daemon:

```sh
curl -fsSL https://raw.githubusercontent.com/ArionMiles/alfred/main/deploy/install.sh | bash
docker compose up -d --wait
```

The installer downloads `docker-compose.yml` and a default `config.toml` into
the current directory. Running it again preserves an existing config file.
Compose uses `ghcr.io/arionmiles/alfred:tip`.

To build from a source checkout instead, run from the repository root:

```sh
docker build -t alfred:local .
ALFRED_IMAGE=alfred:local docker compose -f deploy/docker-compose.yml up -d --wait
```

The commands below assume the installer directory. In a source checkout, use
`docker compose -f deploy/docker-compose.yml` in place of `docker compose`.

Set `ALFRED_IMAGE` to select a versioned image or a pinned `tip-<sha>` build.
Set `ALFRED_PORT` to change the host port from 8080. The published port is bound
to the host's localhost. The daemon has no authentication; keep it on a trusted
network.

The downloaded `config.toml` is mounted read-only. Edit that file for server
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

For a source checkout, rebuild with `docker build -t alfred:local .`, then run
`ALFRED_IMAGE=alfred:local docker compose -f deploy/docker-compose.yml up -d`.

```sh
docker compose logs -f alfred
docker compose down
```

Stopping the deployment retains the data volume. `docker compose down --volumes`
also deletes saved conversations, settings, and credentials.
