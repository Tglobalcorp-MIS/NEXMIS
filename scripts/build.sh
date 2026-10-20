#!/usr/bin/env bash
# Builds the web UI, nexmis-server and nexmis-agent, and publishes the agent
# binaries with manifest.json under dist/agent (what the server serves to
# agents for installation and self-update).
#
#   VERSION=1.0.0 scripts/build.sh            # everything
#   SKIP_WEB=1 scripts/build.sh               # reuse web/dist
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OUT="${OUT:-dist}"
export CGO_ENABLED=0

echo "==> NEXMIS ${VERSION}"
rm -rf "${OUT}"
mkdir -p "${OUT}/agent"

if [[ -z "${SKIP_WEB:-}" ]]; then
  echo "==> web"
  (cd web && npm ci --no-audit --no-fund && npm run build)
fi

SERVER_LDFLAGS="-s -w -X github.com/tglobalcorp-mis/nexmis/internal/server/core.Version=${VERSION} -X github.com/tglobalcorp-mis/nexmis/internal/server/core.BuildTime=${BUILD_TIME}"
for target in linux/amd64 linux/arm64 windows/amd64; do
  os="${target%/*}"; arch="${target#*/}"
  ext=""; [[ "$os" == windows ]] && ext=".exe"
  echo "==> nexmis-server ${os}/${arch}"
  GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "${SERVER_LDFLAGS}" \
    -o "${OUT}/nexmis-server-${os}-${arch}${ext}" ./cmd/nexmis-server
done

AGENT_LDFLAGS="-s -w -X main.Version=${VERSION}"
builds=()
for arch in amd64 arm64; do
  echo "==> nexmis-agent windows/${arch}"
  file="nexmis-agent-windows-${arch}.exe"
  GOOS=windows GOARCH="$arch" go build -trimpath -ldflags "${AGENT_LDFLAGS}" -o "${OUT}/agent/${file}" ./cmd/nexmis-agent
  sha="$(sha256sum "${OUT}/agent/${file}" | cut -d' ' -f1)"
  size="$(stat -c %s "${OUT}/agent/${file}")"
  builds+=("\"windows/${arch}\": {\"file\": \"${file}\", \"sha256\": \"${sha}\", \"size\": ${size}}")
done
# install-agent.ps1 must keep its UTF-8 BOM: Windows PowerShell 5.1 reads a
# BOM-less script in the ANSI code page and fails on the Chinese messages.
if [[ "$(head -c3 deploy/agent/install-agent.ps1 | od -An -tx1 | tr -d ' \n')" != efbbbf ]]; then
  echo "deploy/agent/install-agent.ps1 must be saved as UTF-8 with BOM" >&2
  exit 1
fi
cp deploy/agent/install-agent.ps1 "${OUT}/agent/"
cp -r deploy/linux "${OUT}/linux"

joined="$(IFS=,; echo "${builds[*]}")"
cat > "${OUT}/agent/manifest.json" <<JSON
{
  "version": "${VERSION}",
  "auto_update": true,
  "builds": { ${joined} }
}
JSON
echo "==> done: ${OUT}/"
ls -la "${OUT}" "${OUT}/agent"
