#!/bin/sh
# Docker integration test.
#
# Builds the image, then uses the docker-compose example to prove that:
#   1. a valid environment  -> heimdall exits 0 and the applications start;
#   2. an invalid environment -> heimdall exits 1 and the applications never start.
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
EXAMPLE="$ROOT/examples/docker-compose"
IMAGE="heimdall-lint:integration-test"
WORK="$(mktemp -d)"
PROJECT="heimdall-it-$$"
FAILED=0

cleanup() {
    docker compose -p "$PROJECT-ok" -f "$EXAMPLE/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
    docker compose -p "$PROJECT-bad" -f "$EXAMPLE/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

check() {
    if [ "$2" = "$3" ]; then
        echo "ok   - $1"
    else
        echo "FAIL - $1 (got '$2', want '$3')"
        FAILED=1
    fi
}

contains() {
    if grep -q -- "$3" "$2"; then
        echo "ok   - $1"
    else
        echo "FAIL - $1 (no '$3' in $2)"
        FAILED=1
    fi
}

echo "== building image"
docker build -q -t "$IMAGE" "$ROOT" >/dev/null
export HEIMDALL_IMAGE="$IMAGE"

echo "== image is minimal"
check "no shell in the image" "$(docker run --rm --entrypoint /bin/sh "$IMAGE" -c true >/dev/null 2>&1 && echo yes || echo no)" "no"
check "runs as non-root" "$(docker inspect -f '{{.Config.User}}' "$IMAGE")" "65534:65534"

echo "== scenario 1: valid environment"
set +e
ENVS_DIR="$EXAMPLE/envs" docker compose -p "$PROJECT-ok" -f "$EXAMPLE/compose.yaml" up --abort-on-container-exit --exit-code-from reports-api >"$WORK/ok.log" 2>&1
OK_EXIT=$?
set -e
check "compose exits 0" "$OK_EXIT" "0"
check "reports-api started" "$(grep -c 'reports-api started with KAFKA' "$WORK/ok.log" || true)" "1"
check "inventory-api started" "$(grep -c 'inventory-api started' "$WORK/ok.log" || true)" "1"

echo "== scenario 2: one invalid file among several"
cp -r "$EXAMPLE/envs" "$WORK/broken"
sed -i 's/DATABASE_PORT=5432/DATABASE_PORT=banana/' "$WORK/broken/inventory-api.env"
set +e
ENVS_DIR="$WORK/broken" docker compose -p "$PROJECT-bad" -f "$EXAMPLE/compose.yaml" up --abort-on-container-exit --exit-code-from heimdall >"$WORK/bad.log" 2>&1
BAD_EXIT=$?
set -e
check "heimdall exits 1" "$BAD_EXIT" "1"
contains "failure names the file" "$WORK/bad.log" "inventory-api.env"
contains "failure names the variable" "$WORK/bad.log" "DATABASE_PORT"
contains "failure shows the received value" "$WORK/bad.log" '"banana"'
check "reports-api never started" "$(grep -c 'reports-api started' "$WORK/bad.log" || true)" "0"
check "inventory-api never started" "$(grep -c 'inventory-api started' "$WORK/bad.log" || true)" "0"

echo "== docker run: manifest mode with a read-only mount"
set +e
docker run --rm -v "$ROOT/examples/manifest:/workspace:ro" "$IMAGE" lint >"$WORK/manifest.log" 2>&1
MANIFEST_EXIT=$?
set -e
check "manifest example exits 0" "$MANIFEST_EXIT" "0"
contains "manifest example validated its files" "$WORK/manifest.log" "Files discovered:     4"

echo "== docker run: Compose file validated by the image"
set +e
docker run --rm -v "$EXAMPLE:/workspace:ro" "$IMAGE" lint compose.yaml --schema-dir schemas >"$WORK/compose.log" 2>&1
COMPOSE_EXIT=$?
set -e
check "compose mode exits 0" "$COMPOSE_EXIT" "0"
contains "compose mode validates each service" "$WORK/compose.log" "compose.yaml \\[service reports-api\\]"

echo "== --image: the contract travels with the application version"
HOST_BIN="$WORK/heimdall"
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$HOST_BIN" ./cmd/heimdall)
IMG="heimdall-it-app"
mkdir -p "$WORK/img1" "$WORK/img2" "$WORK/img3"
printf 'version: 1\nvariables:\n  DATABASE_PORT: {type: port, required: true}\n  API_TIMEOUT: {type: duration, default: 5s}\n' > "$WORK/img1/env.schema.yaml"
printf 'version: 1\nvariables:\n  DATABASE_PORT: {type: port, required: true}\n  API_TIMEOUT: {type: duration, default: 10s}\n  API_READ_TIMEOUT: {type: duration, required: true}\n' > "$WORK/img2/env.schema.yaml"
printf 'version: 1\nvariables:\n  ONLY_HERE: {type: string, required: true}\n' > "$WORK/img3/custom.yaml"
for v in 1 2; do
    printf 'FROM scratch\nCOPY env.schema.yaml /env.schema.yaml\n' > "$WORK/img$v/Dockerfile"
    docker build -q -t "$IMG:v$v" "$WORK/img$v" >/dev/null
done
printf 'FROM scratch\nLABEL io.heimdall.schema=/etc/app/custom.yaml\nCOPY custom.yaml /etc/app/custom.yaml\n' > "$WORK/img3/Dockerfile"
docker build -q -t "$IMG:labelled" "$WORK/img3" >/dev/null
printf 'FROM scratch\nLABEL nothing=here\nCOPY custom.yaml /other.yaml\n' > "$WORK/img3/Dockerfile.noschema"
docker build -q -f "$WORK/img3/Dockerfile.noschema" -t "$IMG:noschema" "$WORK/img3" >/dev/null
trap 'docker rmi -f "$IMG:v1" "$IMG:v2" "$IMG:labelled" "$IMG:noschema" >/dev/null 2>&1 || true; cleanup' EXIT

printf 'DATABASE_PORT=5432\n' > "$WORK/current.env"
set +e
"$HOST_BIN" lint --image "$IMG:v1" "$WORK/current.env" >"$WORK/v1.log" 2>&1; V1_EXIT=$?
"$HOST_BIN" lint --image "$IMG:v2" "$WORK/current.env" >"$WORK/v2.log" 2>&1; V2_EXIT=$?
"$HOST_BIN" diff "image://$IMG:v1" "image://$IMG:v2" >"$WORK/diff.log" 2>&1; DIFF_EXIT=$?
"$HOST_BIN" inspect API_READ_TIMEOUT --image "$IMG:v2" >"$WORK/inspect.log" 2>&1; INSPECT_EXIT=$?
"$HOST_BIN" lint --image "$IMG:labelled" "$WORK/current.env" >"$WORK/labelled.log" 2>&1; LABEL_EXIT=$?
"$HOST_BIN" lint --image "$IMG:noschema" "$WORK/current.env" >"$WORK/noschema.log" 2>&1; NOSCHEMA_EXIT=$?
"$HOST_BIN" lint --image "heimdall-it-does-not-exist:1" "$WORK/current.env" >"$WORK/ghost.log" 2>&1; GHOST_EXIT=$?
set -e
check "same env passes against v1" "$V1_EXIT" "0"
check "same env FAILS against v2 (version-aware)" "$V2_EXIT" "1"
contains "v2 names the new required variable" "$WORK/v2.log" "API_READ_TIMEOUT"
contains "schema path shows the image" "$WORK/v2.log" "image://$IMG:v2"
check "diff between two images exits 0" "$DIFF_EXIT" "0"
contains "diff flags the action required" "$WORK/diff.log" "API_READ_TIMEOUT  \\[action required\\]"
contains "diff shows the default change" "$WORK/diff.log" "default: 5s -> 10s"
check "inspect --image exits 0" "$INSPECT_EXIT" "0"
contains "inspect reads from the image" "$WORK/inspect.log" "Type:           duration"
contains "io.heimdall.schema label is honored" "$WORK/labelled.log" "ONLY_HERE"
check "image without a schema fails closed" "$NOSCHEMA_EXIT" "1"
contains "missing schema in image is explicit" "$WORK/noschema.log" "Schema not found"
check "missing image is a contract failure" "$GHOST_EXIT" "3"
contains "missing image explains itself" "$WORK/ghost.log" "present locally"
check "no containers left behind" "$(docker ps -a -q --filter "ancestor=$IMG:v1" --filter "ancestor=$IMG:v2" --filter "ancestor=$IMG:labelled" --filter "ancestor=$IMG:noschema" | wc -l | tr -d ' ')" "0"

[ "$FAILED" = "0" ] && echo "All Docker integration checks passed." || { echo "Docker integration checks FAILED."; exit 1; }
