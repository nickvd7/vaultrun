#!/usr/bin/env bash
# VaultRun evidence demo: an AI agent runs code in a sandbox, the session is
# exported as a signed evidence pack, an auditor verifies it offline, and a
# single changed byte is caught.
#
# Needs a running VaultRun (make up) with EVIDENCE_SIGNING_KEY set (make up
# generates it), the vaultrun CLI, curl and jq:
#
#   export VAULTRUN_API_URL=http://localhost:8080 VAULTRUN_API_KEY=vr_...
#   examples/evidence-demo/demo.sh
#
# Set EVIDENCE_TSA_URL on the server (e.g. http://timestamp.digicert.com) to
# also see an external RFC 3161 timestamp (DEMO_TSA_CA=<pem> for a TSA whose
# root is not in the system store). Files land in ./evidence-demo/.
set -euo pipefail

API=${VAULTRUN_API_URL:-http://localhost:8080}
KEY=${VAULTRUN_API_KEY:?set VAULTRUN_API_KEY to an API key (make bootstrap-key)}
CLI=${VAULTRUN_CLI:-vaultrun}
OUT=${DEMO_DIR:-evidence-demo}
for tool in curl jq "$CLI"; do
  command -v "$tool" >/dev/null || { echo "missing: $tool" >&2; exit 1; }
done

bold=$(tput bold 2>/dev/null || true); dim=$(tput dim 2>/dev/null || true); reset=$(tput sgr0 2>/dev/null || true)
step() { printf '\n%s== %s%s\n' "$bold" "$*" "$reset"; }
note() { printf '%s%s%s\n' "$dim" "$*" "$reset"; }
api() {
  local method=$1 path=$2 body=${3:-}
  curl -fsS -X "$method" "$API$path" -H "X-API-Key: $KEY" -H 'Content-Type: application/json' ${body:+-d "$body"}
}
mkdir -p "$OUT"

step "1. An AI agent gets a sandbox (no network, read-only root filesystem)"
SESSION=$(api POST /api/v1/sessions '{"image":"python:3.12-slim","name":"invoice-agent","network_enabled":false}' | jq -r .id)
note "session $SESSION"

step "2. The agent runs the code it wrote"
api POST "/api/v1/sessions/$SESSION/run" "$(jq -n '{command:"python", args:["-c",
  "rows=[(\"INV-1\",120),(\"INV-2\",80)]; open(\"/workspace/report.csv\",\"w\").write(\"invoice,amount\\n\"+\"\".join(f\"{i},{a}\\n\" for i,a in rows)); print(\"total\", sum(a for _,a in rows))"]}')" \
  | jq -r '"exit \(.exit_code): \(.stdout)"'

step "3. The session is exported as a signed evidence pack"
"$CLI" --api-url "$API" --api-key "$KEY" evidence export "$SESSION" -o "$OUT/pack.json"
"$CLI" --api-url "$API" --api-key "$KEY" evidence public-key > "$OUT/vaultrun-evidence.pub" 2>/dev/null
note "the auditor gets two files: $OUT/pack.json and the public key $OUT/vaultrun-evidence.pub"

step "4. The auditor verifies it offline: no server, no API key"
env -u VAULTRUN_API_URL -u VAULTRUN_API_KEY "$CLI" evidence verify "$OUT/pack.json" --key "$OUT/vaultrun-evidence.pub" \
  ${DEMO_TSA_CA:+--tsa-ca "$DEMO_TSA_CA"} || true

step "5. Someone changes one character of the recorded output"
jq '.pack |= (@base64d | sub("total 200"; "total 100") | @base64)' "$OUT/pack.json" > "$OUT/tampered.json"
if env -u VAULTRUN_API_URL -u VAULTRUN_API_KEY "$CLI" evidence verify "$OUT/tampered.json" --key "$OUT/vaultrun-evidence.pub"; then
  echo "unexpected: the tampered pack verified" >&2
  exit 1
fi
note "caught: the signature covers every byte of the pack"

step "Done"
api DELETE "/api/v1/sessions/$SESSION" >/dev/null
note "session deleted; its runs and audit entries stay, so the pack can be exported again"
