#!/bin/sh
# Validates every environment file in ./envs, each against its own schema.
# Expected: inventory-api.env fails, notification.env warns, exit code 1.
cd "$(dirname "$0")" || exit 2
HEIMDALL="${HEIMDALL:-heimdall}"

"$HEIMDALL" lint --all envs --schema-dir schemas
echo "exit code: $?"
