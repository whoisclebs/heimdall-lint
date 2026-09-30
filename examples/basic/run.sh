#!/bin/sh
# Validates one file against ./env.schema.yaml.
# Expected: production.env passes (exit 0), broken.env fails (exit 1).
cd "$(dirname "$0")" || exit 2
HEIMDALL="${HEIMDALL:-heimdall}"

"$HEIMDALL" lint production.env
echo "exit code: $?"
echo
"$HEIMDALL" lint broken.env
echo "exit code: $?"
