#!/usr/bin/env bash
# Generates the command-line client for the read-only daemon.
#
# mcporter (openclaw/mcporter, pinned in cli/package.json) reads the tool list
# from a running daemon and writes two files:
#
#   cli/whatsapp.ts    the generated source, checked in so a change to the
#                      tools shows up in review
#   bin/whatsapp.mjs   the bundled client, run with Node.js 24 or later
#
# The client embeds the daemon URL, http://127.0.0.1:8765/mcp by default; set
# WHATSAPP_MCP_ADDR to embed another host and port. It holds only the tools a
# read-only daemon serves, so it has no command that sends or changes anything.
#
# The script runs the daemon on an empty store, which would dial WhatsApp to
# pair. So it re-runs itself inside a new network namespace (unshare -rn),
# which has only loopback, and refuses to start the daemon while any route
# leads off the machine. If unshare fails, it exits before the daemon starts.
#
# Usage: scripts/gen-cli.sh [path/to/whatsapp-mcp]
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
bin=$(realpath "${1:-$repo/bin/whatsapp-mcp}")
addr=${WHATSAPP_MCP_ADDR:-127.0.0.1:8765}
mcporter=$repo/cli/node_modules/.bin/mcporter

# Routes that leave the machine, that is every route not on loopback.
outside_routes() { { ip route show; ip -6 route show; } | grep -vw 'dev lo' || true; }

if [[ -n "$(outside_routes)" ]]; then
  if [[ -n "${GEN_CLI_ISOLATED:-}" ]]; then
    echo "gen-cli: routes off the machine inside the namespace:" >&2
    outside_routes >&2
    exit 1
  fi
  exec unshare -rn env GEN_CLI_ISOLATED=1 "$0" "$bin"
fi

for tool in "$bin" "$mcporter"; do
  if [[ ! -x "$tool" ]]; then
    echo "gen-cli: $tool not found; run make build and npm ci --prefix cli" >&2
    exit 1
  fi
done

ip link set lo up
# Loopback calls must not go through a proxy.
unset HTTPS_PROXY HTTP_PROXY https_proxy http_proxy ALL_PROXY all_proxy

store=$(mktemp -d)
"$bin" -store "$store" serve -read-only -addr "$addr" >"$store/serve.log" 2>&1 &
daemon=$!
trap 'kill "$daemon" 2>/dev/null; wait "$daemon" 2>/dev/null; rm -rf "$store"' EXIT

for _ in $(seq 100); do
  curl -sf "http://$addr/healthz" >/dev/null && break
  if ! kill -0 "$daemon" 2>/dev/null; then
    echo "gen-cli: daemon exited; its log:" >&2
    cat "$store/serve.log" >&2
    exit 1
  fi
  sleep 0.1
done

cd "$repo"
mkdir -p bin
"$mcporter" generate-cli \
  --command "http://$addr/mcp" \
  --name whatsapp \
  --runtime node \
  --output cli/whatsapp.ts \
  --bundle bin/whatsapp.mjs
