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
# WHATSAPP_MCP_ADDR to embed another host and port. It holds the tools a
# read-only daemon serves with default settings: no send or group commands, and
# no label commands, which need -enable-labels.
#
# The script runs the daemon on an empty store, which would dial WhatsApp to
# pair. So it always re-runs itself inside a new network namespace
# (unshare -rn), and there refuses to start the daemon while any route leads off
# the machine. If unshare fails, it exits before the daemon starts.
#
# Usage: scripts/gen-cli.sh [path/to/whatsapp-mcp]
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
bin=$(realpath "${1:-$repo/bin/whatsapp-mcp}")
addr=${WHATSAPP_MCP_ADDR:-127.0.0.1:8765}
mcporter=$repo/cli/node_modules/.bin/mcporter

# GEN_CLI_ISOLATED marks the re-run; it is not meant to be set by hand.
if [[ -z "${GEN_CLI_ISOLATED:-}" ]]; then
  exec unshare -rn env GEN_CLI_ISOLATED=1 WHATSAPP_MCP_ADDR="$addr" "$0" "$bin"
fi

# Routes that leave the machine, in every routing table: any not on loopback.
# Each assignment stops the script, under set -e, if ip fails.
routes4=$(ip -4 route show table all)
routes6=$(ip -6 route show table all)
outside=$(printf '%s\n%s\n' "$routes4" "$routes6" | grep -vw 'dev lo' | grep . || true)
if [[ -n "$outside" ]]; then
  echo "gen-cli: routes off the machine inside the namespace:" >&2
  echo "$outside" >&2
  exit 1
fi
ip link set lo up

for tool in "$bin" "$mcporter"; do
  if [[ ! -x "$tool" ]]; then
    echo "gen-cli: $tool not found; run make build and npm ci --prefix cli" >&2
    exit 1
  fi
done

# Loopback calls must not go through a proxy, and the daemon's other settings,
# such as WHATSAPP_MCP_LABELS, must not change the generated tools.
unset HTTPS_PROXY HTTP_PROXY https_proxy http_proxy ALL_PROXY all_proxy
while read -r name; do unset "$name"; done < <(compgen -e | grep '^WHATSAPP_MCP_' || true)

# The health check below must reach this script's daemon, not another one.
if curl -s -o /dev/null "http://$addr/healthz"; then
  echo "gen-cli: something already answers on $addr" >&2
  exit 1
fi

store=$(mktemp -d)
"$bin" -store "$store" serve -read-only -addr "$addr" >"$store/serve.log" 2>&1 &
daemon=$!
trap 'kill "$daemon" 2>/dev/null; wait "$daemon" 2>/dev/null; rm -rf "$store"' EXIT

ready=
for _ in $(seq 100); do
  if ! kill -0 "$daemon" 2>/dev/null; then
    echo "gen-cli: daemon exited; its log:" >&2
    cat "$store/serve.log" >&2
    exit 1
  fi
  if curl -sf "http://$addr/healthz" >/dev/null; then
    ready=1
    break
  fi
  sleep 0.1
done
if [[ -z "$ready" ]]; then
  echo "gen-cli: daemon not healthy after 10 s; its log:" >&2
  cat "$store/serve.log" >&2
  exit 1
fi

cd "$repo"
mkdir -p bin
"$mcporter" generate-cli \
  --command "http://$addr/mcp" \
  --name whatsapp \
  --runtime node \
  --output cli/whatsapp.ts \
  --bundle bin/whatsapp.mjs
