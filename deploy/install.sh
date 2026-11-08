#!/usr/bin/env bash
set -euo pipefail

base_url="https://raw.githubusercontent.com/ArionMiles/alfred/main/deploy"

curl -fsSL "$base_url/docker-compose.yml" -o docker-compose.yml
if [[ ! -e config.toml ]]; then
  curl -fsSL "$base_url/config.toml" -o config.toml
fi
