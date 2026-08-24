#!/usr/bin/env bash
# Deterministic smoke test for the CacaoFerment backend.
#
# Builds the server, starts it against a temporary SQLite directory, probes the
# local health and lock/audit endpoints, then tears everything down. It never
# reaches the external network and does not call go test.
set -euo pipefail

PORT="${SMOKE_PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"

# Keep every temporary artifact inside the repository so cleanup never touches
# anything outside the project directory.
WORK_DIR="$(mktemp -d ./cacaoferment-smoke.XXXXXX)"
BIN="${WORK_DIR}/cacaofermentd"
DATA_DIR="${WORK_DIR}/data"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]]; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "==> building cacaofermentd"
go build -o "${BIN}" ./cmd/cacaofermentd

echo "==> starting server on ${BASE}"
DATA_DIR="${DATA_DIR}" ADDR="127.0.0.1:${PORT}" "${BIN}" &
SERVER_PID=$!

health=""
for _ in $(seq 1 100); do
  if health="$(curl -fsS "${BASE}/api/health" 2>/dev/null)"; then
    break
  fi
  sleep 0.1
done

if [[ -z "${health}" ]]; then
  echo "server did not become healthy" >&2
  exit 1
fi
if ! grep -q '"status":"ok"' <<<"${health}"; then
  echo "unexpected health response: ${health}" >&2
  exit 1
fi
echo "==> health OK: ${health}"

echo "==> locking a joint-inspection task"
lock_resp="$(curl -fsS -X POST "${BASE}/api/tasks/lock" \
  -H 'Content-Type: application/json' \
  -d '{"rule_version":"v1","plot_id":"plot-1","variety_batch_id":"variety-criollo-1","bin_ids":["bin-1","bin-2"],"probe_ids":["probe-1"],"plate_well_ids":["well-1"],"drying_window_id":"window-1","boxed_weight_grams":120000,"blind_samples":[{"blind_code":"BC-1","sample_size":100,"bin_id":"bin-1"},{"blind_code":"BC-2","sample_size":100,"bin_id":"bin-2"}]}')"

task_id="$(grep -o '"TaskID":"[^"]*"' <<<"${lock_resp}" | head -n1 | cut -d'"' -f4)"
if [[ -z "${task_id}" ]]; then
  echo "lock did not return a task id: ${lock_resp}" >&2
  exit 1
fi
echo "==> locked task ${task_id}"

echo "==> reading audit trail"
audit_resp="$(curl -fsS "${BASE}/api/tasks/${task_id}/audit")"
if ! grep -q '"task"' <<<"${audit_resp}"; then
  echo "audit response invalid: ${audit_resp}" >&2
  exit 1
fi
if ! grep -q '"leases"' <<<"${audit_resp}"; then
  echo "audit response missing leases: ${audit_resp}" >&2
  exit 1
fi
echo "==> audit OK (leases present)"

echo "==> smoke test passed"
