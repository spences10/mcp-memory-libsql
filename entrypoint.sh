#!/bin/bash
set -euo pipefail

# entrypoint.sh - choose runtime args based on MODE env var
# MODE: single | multi | voyageai

# If the container was started with explicit args (CMD/compose command),
# honor those and exec the binary directly with provided args. This lets
# docker-compose's `command:` override behavior work as expected.
if [ "$#" -gt 0 ]; then
  exec /usr/local/bin/mcp-memory-libsql-go "$@"
fi

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
