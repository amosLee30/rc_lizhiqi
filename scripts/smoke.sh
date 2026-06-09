#!/usr/bin/env bash
# End-to-end smoke test: fake upstream + gateway + submit/query/metrics.
set -uo pipefail
cd "$(dirname "$0")/.."

# Bypass any local SOCKS/HTTP proxy for loopback calls.
unset ALL_PROXY all_proxy HTTP_PROXY http_proxy HTTPS_PROXY https_proxy
export NO_PROXY='*' no_proxy='*'

WORK="$(mktemp -d)"
trap 'kill ${UP_PID:-0} ${GW_PID:-0} 2>/dev/null; rm -rf "$WORK"' EXIT

# 1) fake upstream supplier on :9099 — always 200
python3 - <<'PY' &
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n=int(self.headers.get('Content-Length',0)); self.rfile.read(n)
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"ok":true}')
    def log_message(self,*a): pass
HTTPServer(('127.0.0.1',9099),H).serve_forever()
PY
UP_PID=$!

# 2) build + run gateway on :8088 with a fresh db
go build -o "$WORK/server" ./cmd/server || exit 1
ADDR=:8088 \
  SUPPLIERS_FILE=suppliers.json \
  AD_TOKEN=ad-tok CRM_TOKEN=crm-tok \
  "$WORK/server" &
GW_PID=$!

# wait for readiness
for i in $(seq 1 30); do
  curl -fsS http://127.0.0.1:8088/healthz >/dev/null 2>&1 && break
  sleep 0.2
done

echo "== submit =="
RESP=$(curl -fsS -X POST http://127.0.0.1:8088/notifications \
  -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"order-1","source_system":"billing","type":"crm-contact","params":{"contactId":"42","status":"paid"}}')
echo "$RESP"
TID=$(echo "$RESP" | python3 -c "import sys,json;print(json.load(sys.stdin)['tracking_id'])")

echo "== idempotent resubmit (same tracking id, 200) =="
curl -fsS -o /dev/null -w "http=%{http_code}\n" -X POST http://127.0.0.1:8088/notifications \
  -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"order-1","source_system":"billing","type":"crm-contact","params":{"contactId":"42","status":"paid"}}'

# let the worker deliver
for i in $(seq 1 20); do
  S=$(curl -fsS "http://127.0.0.1:8088/notifications/$TID" | python3 -c "import sys,json;print(json.load(sys.stdin)['status'])")
  [ "$S" = "DELIVERED" ] && break
  sleep 0.3
done

echo "== coarse status =="
curl -fsS "http://127.0.0.1:8088/notifications/$TID"; echo
echo "== detail status (ops) =="
curl -fsS -H 'Authorization: Bearer ops-secret' "http://127.0.0.1:8088/notifications/$TID?detail=true"; echo
echo "== unknown type rejected =="
curl -sS -o /dev/null -w "http=%{http_code}\n" -X POST http://127.0.0.1:8088/notifications \
  -H 'Content-Type: application/json' -d '{"idempotency_key":"x","source_system":"s","type":"ghost","params":{}}'
echo "== metrics =="
curl -fsS http://127.0.0.1:8088/metrics; echo
