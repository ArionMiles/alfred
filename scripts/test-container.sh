#!/bin/sh
set -eu

project="alfred-test-$$"
export ALFRED_IMAGE="alfred-test:$$" ALFRED_PORT=0
compose() { docker compose -p "$project" "$@"; }
cleanup() {
  result=$?
  if test "$result" -ne 0; then compose logs || true; fi
  compose down --volumes --remove-orphans || true
  docker image rm "$ALFRED_IMAGE" >/dev/null || true
  exit "$result"
}
trap cleanup EXIT
compose build
compose up -d --wait --wait-timeout 90
endpoint="http://$(compose port alfred 8080)"
resolve() {
  curl -fsS --max-time 10 -H 'Content-Type: application/json' -d '{"root":"/var/lib/alfred/workspace"}' \
    "$endpoint/alfred.v1.ProjectService/ResolveProject"
}
before=$(resolve)
compose exec -T alfred /bin/zsh -dfc 'test "$PWD" = /var/lib/alfred/workspace && test -w . && print persisted > marker'
compose up -d --force-recreate --wait --wait-timeout 90
endpoint="http://$(compose port alfred 8080)"
after=$(resolve)
test "$before" = "$after"
test "$(compose exec -T alfred cat marker)" = persisted
compose stop
container=$(compose ps -aq alfred)
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
printf '%s\n' 'Container RPC, runtime, persistence, and shutdown checks passed.'
