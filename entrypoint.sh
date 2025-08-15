#!/bin/bash
set -euo pipefail

# entrypoint.sh - choose runtime args based on MODE env var
# MODE: single | multi | voyageai

# Preference: if MODE is explicitly provided (non-empty), it takes precedence
# over any CMD/compose `command:`. If MODE is not provided and CMD args exist,
# honor the CMD args. If neither MODE nor CMD are provided, default to single.
mode_provided=0
if [ "${MODE+set}" = "set" ] && [ -n "${MODE}" ]; then
    mode_provided=1
fi

if [ "$mode_provided" -eq 0 ]; then
    # MODE not explicitly provided; if CMD args exist, execute them
    if [ "$#" -gt 0 ]; then
        exec /usr/local/bin/mcp-memory-libsql-go "$@"
    fi
fi

# Determine MODE and defaults (if MODE was provided use it, else default to single)
MODE=${MODE:-single}
PORT=${PORT:-8080}
METRICS_PORT=${METRICS_PORT:-9090}
PROJECTS_DIR=${PROJECTS_DIR:-/data/projects}

COMMON_ARGS=("-transport" "${TRANSPORT:-sse}" "-addr" ":${PORT}" "-sse-endpoint" "${SSE_ENDPOINT:-/sse}")

case "$MODE" in
single)
    exec /usr/local/bin/mcp-memory-libsql-go "${COMMON_ARGS[@]}"
    ;;
multi)
    exec /usr/local/bin/mcp-memory-libsql-go "${COMMON_ARGS[@]}" -projects-dir "${PROJECTS_DIR}"
    ;;
voyageai)
    # voyageai uses same multi-project flags but expects VOYAGE env vars to be present
    exec /usr/local/bin/mcp-memory-libsql-go "${COMMON_ARGS[@]}" -projects-dir "${PROJECTS_DIR}"
    ;;
*)
    echo "Unknown MODE='${MODE}' - expected single|multi|voyageai" >&2
    exit 2
    ;;
esac
