#!/usr/bin/env bash
# End-to-end runner for steve-tests.
#
# Builds Steve at a release tag, runs it against a throwaway k3d cluster, and
# runs the entire suite with mutations, workloads, and CRDs enabled. The GitHub
# workflow calls the same subcommands, so a local run reproduces CI.
#
# Usage:
#   STEVE_VERSION=v0.10.9 scripts/e2e.sh all    # up, test, down
#   STEVE_VERSION=v0.10.9 scripts/e2e.sh up     # cluster + Steve only
#   scripts/e2e.sh test                         # run the suite against "up"
#   scripts/e2e.sh down                         # stop Steve, delete cluster
#
# Settings (environment):
#   STEVE_VERSION       Steve tag to build, with leading v (required for up/all).
#   STEVE_SOURCE_DIR    Build this existing Steve checkout instead of cloning.
#   STEVE_REPO          Git URL to clone (default https://github.com/rancher/steve.git).
#   E2E_DIR             Work directory (default <repo>/.e2e).
#   E2E_CLUSTER         k3d cluster name (default steve-e2e).
#   E2E_K3S_IMAGE       k3s image for the cluster (default: k3d's default).
#   E2E_HTTP_PORT       Steve HTTP port (default 9080).
#   E2E_HTTPS_PORT      Steve HTTPS port (default 9443).
#   E2E_READY_TIMEOUT   Seconds to wait for Steve to serve (default 300).
#   E2E_PACKAGES        Packages to test (default ./...).
#   E2E_RUN             Optional -run regex.
#   E2E_TEST_TIMEOUT    go test -timeout (default 30m).
#   E2E_RESULTS_DIR     Where results.json and results.xml go (default <repo>).
#   STEVE_TEST_POD_IMAGE  Workload image to preload (default busybox:1.36).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
E2E_DIR="${E2E_DIR:-$REPO_ROOT/.e2e}"
E2E_CLUSTER="${E2E_CLUSTER:-steve-e2e}"
E2E_HTTP_PORT="${E2E_HTTP_PORT:-9080}"
E2E_HTTPS_PORT="${E2E_HTTPS_PORT:-9443}"
E2E_READY_TIMEOUT="${E2E_READY_TIMEOUT:-300}"
E2E_PACKAGES="${E2E_PACKAGES:-./...}"
E2E_RUN="${E2E_RUN:-}"
E2E_TEST_TIMEOUT="${E2E_TEST_TIMEOUT:-30m}"
E2E_RESULTS_DIR="${E2E_RESULTS_DIR:-$REPO_ROOT}"
STEVE_REPO="${STEVE_REPO:-https://github.com/rancher/steve.git}"
WORKLOAD_IMAGE="${STEVE_TEST_POD_IMAGE:-busybox:1.36}"

KUBECONFIG_FILE="$E2E_DIR/kubeconfig"
STEVE_BIN="$E2E_DIR/bin/steve"
STEVE_LOG="$E2E_DIR/steve.log"
STEVE_PID_FILE="$E2E_DIR/steve.pid"
# Steve redirects plain HTTP to HTTPS, so the suite talks to the HTTPS port.
# Standalone Steve serves a self-signed certificate.
STEVE_URL_LOCAL="https://127.0.0.1:${E2E_HTTPS_PORT}"
CONFIG_FILE="$E2E_DIR/steve-config.yaml"
INFO_FILE="$E2E_DIR/steve-info.env"

log() { printf '[e2e] %s\n' "$*" >&2; }
die() {
  if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
    printf '::error::%s\n' "$*" >&2
  else
    printf '[e2e] ERROR: %s\n' "$*" >&2
  fi
  exit 1
}

require() {
  local cmd
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || die "required command not found: $cmd"
  done
}

# Exports a variable to later GitHub Actions steps when running in CI.
export_github_env() {
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    printf '%s=%s\n' "$1" "$2" >>"$GITHUB_ENV"
  fi
}

validate_version() {
  [[ -n "${STEVE_VERSION:-}" ]] || die "STEVE_VERSION is required (for example v0.10.9)"
  if [[ ! "$STEVE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    die "invalid Steve version: $STEVE_VERSION"
  fi
}

steve_running() {
  [[ -f "$STEVE_PID_FILE" ]] && kill -0 "$(cat "$STEVE_PID_FILE")" 2>/dev/null
}

cluster_exists() {
  k3d cluster list -o json 2>/dev/null | jq -e --arg name "$E2E_CLUSTER" 'any(.[]; .name == $name)' >/dev/null
}

fetch_steve_source() {
  if [[ -n "${STEVE_SOURCE_DIR:-}" ]]; then
    [[ -f "$STEVE_SOURCE_DIR/main.go" ]] || die "STEVE_SOURCE_DIR does not look like a Steve checkout: $STEVE_SOURCE_DIR"
    log "Using existing Steve checkout at $STEVE_SOURCE_DIR"
    STEVE_SRC="$STEVE_SOURCE_DIR"
    return
  fi

  if ! git ls-remote --exit-code --tags "$STEVE_REPO" "refs/tags/$STEVE_VERSION" >/dev/null; then
    die "Steve tag $STEVE_VERSION does not exist in $STEVE_REPO"
  fi

  STEVE_SRC="$E2E_DIR/steve"
  rm -rf "$STEVE_SRC"
  log "Cloning Steve $STEVE_VERSION"
  git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$STEVE_VERSION" "$STEVE_REPO" "$STEVE_SRC"
}

build_steve() {
  log "Building Steve from $STEVE_SRC"
  mkdir -p "$(dirname "$STEVE_BIN")"
  # The caller's GOFLAGS (for example -mod=vendor) must not apply to Steve, and
  # GOTOOLCHAIN=auto fetches a newer Go if the Steve tag requires one.
  (cd "$STEVE_SRC" && GOFLAGS='' GOTOOLCHAIN=auto CGO_ENABLED=0 go build -o "$STEVE_BIN" .)

  STEVE_COMMIT="$(git -C "$STEVE_SRC" rev-parse HEAD 2>/dev/null || echo unknown)"
  STEVE_GO_VERSION="$(go version -m "$STEVE_BIN" | awk 'NR==1 {print $2}')"
}

create_cluster() {
  if cluster_exists; then
    die "k3d cluster $E2E_CLUSTER already exists; run 'scripts/e2e.sh down' first"
  fi

  local args=(cluster create "$E2E_CLUSTER" --wait --timeout 300s --no-lb
    --kubeconfig-update-default=false --kubeconfig-switch-context=false)
  if [[ -n "${E2E_K3S_IMAGE:-}" ]]; then
    args+=(--image "$E2E_K3S_IMAGE")
  fi

  log "Creating k3d cluster $E2E_CLUSTER"
  k3d "${args[@]}"
  k3d kubeconfig get "$E2E_CLUSTER" >"$KUBECONFIG_FILE"
  chmod 600 "$KUBECONFIG_FILE"

  log "Waiting for the cluster to accept workloads"
  kubectl --kubeconfig "$KUBECONFIG_FILE" wait --for=condition=Ready nodes --all --timeout=180s
  wait_for 120 "default service account" \
    kubectl --kubeconfig "$KUBECONFIG_FILE" get serviceaccount default -n default
  kubectl --kubeconfig "$KUBECONFIG_FILE" -n kube-system rollout status deployment/coredns --timeout=180s

  KUBE_VERSION="$(kubectl --kubeconfig "$KUBECONFIG_FILE" version -o json | jq -r '.serverVersion.gitVersion')"
}

# Pulls the workload image into the node before the suite starts, so a
# registry problem shows up here instead of as pod timeouts. Pulling inside the
# node avoids k3d image import, which fails on multi-arch images from Docker's
# containerd image store. A failure is not fatal: pods can still pull later.
preload_workload_image() {
  local node="k3d-${E2E_CLUSTER}-server-0" attempt
  log "Pulling workload image $WORKLOAD_IMAGE into $node"
  for attempt in 1 2 3; do
    if docker exec "$node" crictl pull "$WORKLOAD_IMAGE" >/dev/null; then
      return
    fi
    sleep $((attempt * 5))
  done
  log "WARNING: could not pull $WORKLOAD_IMAGE ahead of time; pods will try again"
}

start_steve() {
  local flags=(--kubeconfig "$KUBECONFIG_FILE"
    --http-listen-port "$E2E_HTTP_PORT"
    --https-listen-port "$E2E_HTTPS_PORT")

  # Through v0.9.x the SQLite cache is opt-in. From v0.10 it is always on and
  # the flag no longer exists. Capture the help text first: piping straight
  # into grep -q can SIGPIPE Steve, and pipefail then reports no match.
  local help
  help="$("$STEVE_BIN" --help 2>&1 || true)"
  if grep -q -- '--sql-cache' <<<"$help"; then
    flags+=(--sql-cache)
    STEVE_SQL_FLAG="--sql-cache"
  else
    STEVE_SQL_FLAG="(always on)"
  fi

  log "Starting Steve: $STEVE_BIN ${flags[*]}"
  # Steve writes its SQLite cache to the working directory, so run it from
  # E2E_DIR. exec keeps the recorded PID pointing at Steve itself.
  (cd "$E2E_DIR" && exec nohup "$STEVE_BIN" "${flags[@]}" >"$STEVE_LOG" 2>&1) &
  echo $! >"$STEVE_PID_FILE"

  local deadline=$((SECONDS + E2E_READY_TIMEOUT))
  until steve_ready; do
    if ! steve_running; then
      tail -n 100 "$STEVE_LOG" >&2 || true
      die "Steve exited before it became ready; see $STEVE_LOG"
    fi
    if ((SECONDS >= deadline)); then
      tail -n 100 "$STEVE_LOG" >&2 || true
      die "Steve did not serve /v1/namespaces within ${E2E_READY_TIMEOUT}s"
    fi
    sleep 2
  done
  log "Steve is serving at $STEVE_URL_LOCAL"
}

# Ready means the API answers with 200; a redirect or error does not count.
steve_ready() {
  [[ "$(curl -ksS -o /dev/null -w '%{http_code}' "$STEVE_URL_LOCAL/v1/namespaces" 2>/dev/null)" == "200" ]]
}

wait_for() {
  local timeout="$1" what="$2"
  shift 2
  local deadline=$((SECONDS + timeout))
  until "$@" >/dev/null 2>&1; do
    ((SECONDS < deadline)) || die "timed out waiting for $what"
    sleep 2
  done
}

write_run_files() {
  # The suite reads this file through STEVE_CONFIG so a developer's own
  # steve-config.yaml can never redirect an e2e run.
  cat >"$CONFIG_FILE" <<EOF
url: "$STEVE_URL_LOCAL"
insecure: true
requestTimeout: "30s"
waitTimeout: "2m"
pollInterval: "200ms"
expectedCacheMode: "sql"
EOF

  cat >"$INFO_FILE" <<EOF
STEVE_VERSION=${STEVE_VERSION:-unknown}
STEVE_COMMIT=$STEVE_COMMIT
STEVE_GO_VERSION=$STEVE_GO_VERSION
STEVE_SQL_FLAG=$STEVE_SQL_FLAG
KUBE_VERSION=$KUBE_VERSION
STEVE_URL=$STEVE_URL_LOCAL
EOF

  export_github_env STEVE_COMMIT "$STEVE_COMMIT"
  export_github_env KUBE_VERSION "$KUBE_VERSION"
  log "Run details:"
  sed 's/^/[e2e]   /' "$INFO_FILE" >&2
}

cmd_up() {
  require git go k3d kubectl docker curl jq
  validate_version
  mkdir -p "$E2E_DIR"
  steve_running && die "Steve is already running (pid $(cat "$STEVE_PID_FILE")); run 'scripts/e2e.sh down' first"

  fetch_steve_source
  build_steve
  create_cluster
  preload_workload_image
  start_steve
  write_run_files
}

cmd_test() {
  require go curl
  [[ -f "$CONFIG_FILE" ]] || die "no e2e environment found in $E2E_DIR; run 'scripts/e2e.sh up' first"
  steve_ready || die "Steve is not serving at $STEVE_URL_LOCAL"

  # Settings from the caller's shell must not change the target.
  unset STEVE_URL STEVE_TOKEN STEVE_CA_FILE STEVE_INSECURE STEVE_INSECURE_SKIP_VERIFY STEVE_EXPECT_CACHE_MODE
  export STEVE_CONFIG="$CONFIG_FILE"
  export STEVE_TEST_ALLOW_MUTATIONS=true
  export STEVE_TEST_ALLOW_WORKLOADS=true
  export STEVE_TEST_ALLOW_CRDS=true
  export STEVE_TEST_POD_IMAGE="$WORKLOAD_IMAGE"

  local go_args=(-count=1 -p=4 -parallel=4 "-timeout=$E2E_TEST_TIMEOUT" -v)
  if [[ -n "$E2E_RUN" ]]; then
    go_args+=(-run "$E2E_RUN")
  fi

  mkdir -p "$E2E_RESULTS_DIR"
  local json="$E2E_RESULTS_DIR/results.json" junit="$E2E_RESULTS_DIR/results.xml"
  log "Running $E2E_PACKAGES with mutations, workloads, and CRDs enabled"

  cd "$REPO_ROOT"
  if command -v gotestsum >/dev/null 2>&1; then
    # shellcheck disable=SC2086 # E2E_PACKAGES may list several packages.
    gotestsum --format standard-verbose --jsonfile "$json" --junitfile "$junit" \
      --packages="$E2E_PACKAGES" -- "${go_args[@]}"
  else
    log "gotestsum not found; writing $json with go test -json (no JUnit file)"
    local status=0
    # shellcheck disable=SC2086 # E2E_PACKAGES may list several packages.
    go test -json "${go_args[@]}" $E2E_PACKAGES >"$json" || status=$?
    jq -r 'select(.Action=="output") | .Output' "$json" | tr -d '\r' || true
    return "$status"
  fi
}

cmd_down() {
  if steve_running; then
    log "Stopping Steve (pid $(cat "$STEVE_PID_FILE"))"
    kill "$(cat "$STEVE_PID_FILE")" 2>/dev/null || true
    for _ in $(seq 1 20); do
      steve_running || break
      sleep 0.5
    done
    steve_running && kill -9 "$(cat "$STEVE_PID_FILE")" 2>/dev/null || true
  fi
  rm -f "$STEVE_PID_FILE"

  if command -v k3d >/dev/null 2>&1 && cluster_exists; then
    log "Deleting k3d cluster $E2E_CLUSTER"
    k3d cluster delete "$E2E_CLUSTER" >/dev/null
  fi
  rm -f "$CONFIG_FILE" "$KUBECONFIG_FILE" "$E2E_DIR"/informer_object_cache.db*
  log "Down. Steve's log is kept at $STEVE_LOG"
}

cmd_all() {
  trap cmd_down EXIT
  cmd_up
  cmd_test
}

case "${1:-}" in
  up) cmd_up ;;
  test) cmd_test ;;
  down) cmd_down ;;
  all) cmd_all ;;
  *)
    sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
