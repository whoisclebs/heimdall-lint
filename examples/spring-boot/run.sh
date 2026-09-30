#!/bin/sh
# Spring property names used where environment variable names are required.
# Expected: application.env passes (exit 0), mistakes.env fails (exit 1).
cd "$(dirname "$0")" || exit 2
HEIMDALL="${HEIMDALL:-heimdall}"

"$HEIMDALL" lint application.env
echo "exit code: $?"
echo
"$HEIMDALL" lint mistakes.env
echo "exit code: $?"
