#!/bin/sh
# heimdall.yaml describes the whole environment; no arguments needed.
# Expected: everything is valid, plus a warning for the undeclared old-reports-api.env (exit 0).
cd "$(dirname "$0")" || exit 2
HEIMDALL="${HEIMDALL:-heimdall}"

"$HEIMDALL" lint
echo "exit code: $?"
