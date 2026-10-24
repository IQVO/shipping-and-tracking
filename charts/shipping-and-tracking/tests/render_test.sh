#!/usr/bin/env bash
# Chart render assertions for shipping-and-tracking: Postgres and Kafka are the
# only runtime, so the mandatory configuration has no silent default, no *_MODE
# or log publisher is ever rendered, the env cmd/api reads is the env the chart
# renders, and no secret (DSN, ship-to key, stopKey secret) reaches the ConfigMap.
# Run: bash charts/shipping-and-tracking/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=shipping-and-tracking
# Dummy values, nothing is ever dialled. The key is 32 zero bytes in base64.
key=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
stop=ci-dummy-stop-key-secret
groups=(vendorConsumerGroup:shipping-and-tracking-vendor
  linehaulConsumerGroup:shipping-and-tracking-linehaul
  lastmileConsumerGroup:shipping-and-tracking-lastmile
  catalogConsumerGroup:shipping-and-tracking-catalog)
base=(--set database.url=postgres://u:pw-canary@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092
  --set "crypto.shipToEncryptionKey=$key" --set "crypto.stopKeySecret=$stop"
  --set config.customerOrderingUrl=http://customer-ordering.example.invalid)
for g in "${groups[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done
# Every optional value set, so every env var the binary can read is rendered.
everything=(-f "$chart_dir/ci/default-values.yaml"
  --set config.customerOrderingTimeout=2s --set config.outboxRelayInterval=2s
  --set config.silenceSweepInterval=10m --set config.housekeepingInterval=2h)

fail() { echo "FAIL: $*" >&2; exit 1; }
tpl() { helm template "$release" "$chart_dir" "$@"; }

configmap="$(tpl "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(tpl "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# The api Deployment mounts the chart ConfigMap with envFrom.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# A tuned config.* value reaches the ConfigMap; the relay cap defaults to 20.
tuned="$(tpl "${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
unlimited="$(tpl "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# Chart vs binary: every env var cmd/api reads is rendered into the Deployment
# when every optional value is set. The names come from the binary's own
# env("...")/Getenv("...") literals and the *_CONSUMER_GROUP constants, so a new
# variable in cmd/api fails this test until the chart renders it.
binary_env() {
  {
    grep -ohE '(Getenv|env)\("[A-Z][A-Z0-9_]*"' "$repo_dir"/cmd/api/main.go "$repo_dir"/cmd/api/config.go \
      | sed -E 's/.*\("//; s/"$//'
    grep -ohE '= "[A-Z][A-Z0-9_]*_CONSUMER_GROUP"' "$repo_dir"/cmd/api/config.go | sed -E 's/= "//; s/"$//'
  } | sort -u
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 15 )) || fail "found only ${#api_vars[@]} env names in cmd/api; the scan is looking in the wrong place"
full_deployment="$(tpl "${everything[@]}" --show-only templates/deployment.yaml)"
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$full_deployment" || fail "the Deployment does not render $var, which cmd/api reads"
done
# Every variable cmd/api names as required is rendered by the default values.
# shellcheck disable=SC2207
required_vars=($(grep -ohE '"[A-Z][A-Z0-9_]* is required' "$repo_dir"/cmd/api/config.go | sed -E 's/^"//; s/ is required//' | sort -u))
(( ${#required_vars[@]} >= 5 )) || fail "found only ${#required_vars[@]} required variables in cmd/api/config.go"
for var in "${required_vars[@]}"; do
  grep -q "name: $var\$" <<<"$api_deployment" || fail "the Deployment does not render $var, which cmd/api requires"
done

# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
full_configmap="$(tpl "${everything[@]}" --show-only templates/configmap.yaml)"
api_known=" ${api_vars[*]} "
while read -r key_name; do
  [[ "$api_known" == *" $key_name "* ]] || fail "ConfigMap key $key_name is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")

env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
[[ "$(env_value "$api_deployment" CUSTOMER_ORDERING_URL)" == "http://customer-ordering.example.invalid" ]] || fail "CUSTOMER_ORDERING_URL is not rendered from config.customerOrderingUrl"
[[ "$(env_value "$api_deployment" EVENT_PUBLISHER)" == "kafka" ]] || fail "EVENT_PUBLISHER must be rendered as kafka"
for kv in VENDOR_CONSUMER_GROUP=shipping-and-tracking-vendor LINEHAUL_CONSUMER_GROUP=shipping-and-tracking-linehaul \
  LASTMILE_CONSUMER_GROUP=shipping-and-tracking-lastmile CATALOG_CONSUMER_GROUP=shipping-and-tracking-catalog; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the Deployment does not render $kv from config.*"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done

# Row 6: the DSN, the ship-to key and the stopKey secret come from Secrets.
for var in DATABASE_URL SHIP_TO_ENCRYPTION_KEY STOP_KEY_SECRET; do
  grep -A4 "name: $var\$" <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "$var must come from a Secret (secretKeyRef)"
done
grep -A8 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' \
  || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:|SHIP_TO_ENCRYPTION_KEY|STOP_KEY_SECRET' <<<"$full_configmap"; then
  fail "a DSN, the ship-to key or the stopKey secret must never land in the ConfigMap"
fi
if grep -qF -e 'pw-canary' -e "$key" -e "$stop" <<<"$configmap"$'\n'"$api_deployment"; then
  fail "a secret value leaked into the ConfigMap or the Deployment (they must live in Secrets only)"
fi
[[ "$(tpl "${base[@]}" --show-only templates/crypto-secret.yaml | grep -c "$key")" == 1 ]] || fail "the crypto Secret does not carry the ship-to key"
# An existing secret replaces the chart-created ones.
ex_args=(--set database.existingSecret=db-sec --set crypto.existingSecret=crypto-sec --set kafka.brokers=k:9092 --set config.customerOrderingUrl=http://c)
for g in "${groups[@]}"; do ex_args+=(--set "config.${g%%:*}=${g#*:}"); done
existing="$(tpl "${ex_args[@]}")"
grep -q 'name: db-sec' <<<"$existing" || fail "database.existingSecret is not referenced"
grep -q 'name: crypto-sec' <<<"$existing" || fail "crypto.existingSecret is not referenced"
if grep -q 'kind: Secret' <<<"$existing"; then fail "no Secret may be created when both existing secrets are set"; fi

# No in-memory / permissive / log mode, anywhere in the render or NOTES.
render_all="$(tpl "${base[@]}" "${everything[@]}")"
if grep -qE '^\s+(- name: )?[A-Z_]+_MODE' <<<"$render_all"; then fail "the render carries a *_MODE variable"; fi
if grep -qiE 'value: "?(permissive|log|off)"?$' <<<"$render_all"; then fail "the render carries permissive/log/off"; fi
if grep -qiE 'in-memory|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi

# Row 7: hardening, probes and the grace period.
for needle in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault'; do
  grep -q "$needle" <<<"$api_deployment" || fail "the Deployment lacks $needle"
done
grep -A1 'drop:' <<<"$api_deployment" | grep -q -- '- ALL' || fail "the Deployment does not drop ALL capabilities"
grep -A3 'livenessProbe:' <<<"$api_deployment" | grep -q 'path: /healthz' || fail "liveness must probe /healthz"
grep -A3 'readinessProbe:' <<<"$api_deployment" | grep -q 'path: /readyz' || fail "readiness must probe /readyz"
grep -q 'terminationGracePeriodSeconds: 45' <<<"$api_deployment" || fail "the default grace period must be 45s"

# The shutdown bounds come from cmd/api/main.go: HTTP (shutdownTimeout) +
# consumers/jobs and relay (workerDrainTimeout, awaited twice) + the drain delay
# (default 5s, config.go). The chart's floor must equal that sum: the grace
# period must be GREATER than it.
const_secs() {
  local v
  v="$(grep -E "^const $1 = [0-9]+ \* time.Second" "$repo_dir/cmd/api/main.go" | sed -E 's/.*= ([0-9]+) \*.*/\1/')"
  [[ -n "$v" ]] || fail "could not read $1 from cmd/api/main.go"
  echo "$v"
}
bounds=$(( $(const_secs shutdownTimeout) + 2 * $(const_secs workerDrainTimeout) ))
default_drain="$(grep -E '^\s+defaultDrainDelay\s+= [0-9]+ \* time.Second' "$repo_dir/cmd/api/config.go" | sed -E 's/.*= ([0-9]+) \*.*/\1/')"
[[ -n "$default_drain" ]] || fail "could not read defaultDrainDelay from cmd/api/config.go"
floor=$(( bounds + default_drain ))
[[ "$(env_value "$api_deployment" SHUTDOWN_DRAIN_DELAY)" == "${default_drain}s" ]] || fail "config.shutdownDrainDelay must default to the binary's ${default_drain}s"

# --- refusals: the mandatory values name themselves ---------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(tpl "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
# args that satisfy everything except the one value a case removes.
mandatory_except() { # mandatory_except <database|kafka|crypto|url|<group key>>
  local skip="$1"
  [[ "$skip" == database ]] || printf '%s\n' --set database.url=postgres://u@h/db
  [[ "$skip" == kafka ]] || printf '%s\n' --set kafka.brokers=k:9092
  [[ "$skip" == crypto ]] || printf '%s\n' --set crypto.shipToEncryptionKey=$key --set crypto.stopKeySecret=$stop
  [[ "$skip" == url ]] || printf '%s\n' --set config.customerOrderingUrl=http://c
  for g in "${groups[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || printf '%s\n' --set "config.${g%%:*}=${g#*:}"
  done
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=() line
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
refusal_without crypto "crypto.shipToEncryptionKey"
refusal_without url "config.customerOrderingUrl"
for g in "${groups[@]}"; do refusal_without "${g%%:*}" "config.${g%%:*}"; done
expect_refusal "no stopKey secret" "crypto.stopKeySecret" "${base[@]}" --set crypto.stopKeySecret=
expect_refusal "an empty group (explicit)" "config.vendorConsumerGroup" "${base[@]}" --set config.vendorConsumerGroup=
expect_refusal "the log publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=log
expect_refusal "the log publisher through extraEnv" "EVENT_PUBLISHER" "${base[@]}" --set 'extraEnv[0].name=EVENT_PUBLISHER' --set 'extraEnv[0].value=log'
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=VENDOR_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a grace period equal to the shutdown sum" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=$floor
expect_refusal "a grace period under the shutdown sum" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=20
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
tpl "${base[@]}" --set terminationGracePeriodSeconds=$(( floor + 1 )) >/dev/null \
  || fail "a grace period of $(( floor + 1 ))s must render"
# A longer drain delay raises the floor.
expect_refusal "a grace period under the sum for a 15s drain delay" "terminationGracePeriodSeconds" \
  "${base[@]}" --set config.shutdownDrainDelay=15s --set terminationGracePeriodSeconds=$(( bounds + 15 ))
tpl "${base[@]}" --set config.shutdownDrainDelay=15 --set terminationGracePeriodSeconds=$(( bounds + 16 )) >/dev/null \
  || fail "a 15s drain delay with a $(( bounds + 16 ))s grace period must render"
expect_refusal "a drain delay that is not whole seconds" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=500ms

# Optional components stay off by default; the service exposes the api only.
default_render="$(tpl "${base[@]}")"
for kind in HorizontalPodAutoscaler Ingress HTTPRoute; do
  if grep -q "kind: $kind" <<<"$default_render"; then fail "$kind must not render with default values"; fi
done
[[ "$(grep -c '^kind: Deployment' <<<"$default_render")" == 1 ]] || fail "the chart must render exactly one Deployment (api)"
for opt in HorizontalPodAutoscaler Ingress HTTPRoute; do
  on="$(tpl "${base[@]}" --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true)"
  grep -q "kind: $opt" <<<"$on" || fail "$opt does not render when enabled"
done

# --- optional components (ADR 0006 mcp, ADR 0007 web remote) -------------------
# mcp and frontend are OFF by default too: a default render names neither.
for c in mcp frontend; do
  if grep -q "app.kubernetes.io/component: $c\$" <<<"$default_render"; then
    fail "the $c component must not render while its enabled flag is false"
  fi
done

# binary_env_of <files>: the env literals one binary reads.
binary_env_of() {
  local f
  for f in "$@"; do
    grep -ohE '[Gg]etenv\("[A-Z][A-Z0-9_]*"' "$f" || true
  done | sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}
# assert_env_rendered <rendered> <label> <vars...>
assert_env_rendered() {
  local rendered="$1" label="$2"; shift 2
  local var
  for var in "$@"; do
    grep -q "name: $var\$" <<<"$rendered" || fail "$label does not render $var, which its binary reads"
  done
}

# mcp: its own env, never the api ConfigMap, never Kafka or a write seam (ADR 0006).
mcp_dep="$(tpl "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-deployment.yaml)"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_dep" || fail "the mcp Deployment was not rendered with mcp.enabled=true"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
if grep -q 'configMapRef:' <<<"$mcp_dep"; then fail "mcp-deployment.yaml must not mount the api ConfigMap"; fi
if grep -Eq 'name: (KAFKA_BROKERS|EVENT_PUBLISHER|OUTBOX_[A-Z_]+|[A-Z]+_CONSUMER_GROUP|SHIP_TO_ENCRYPTION_KEY|STOP_KEY_SECRET|CUSTOMER_ORDERING_URL)$' <<<"$mcp_dep"; then
  fail "the mcp Deployment must not get Kafka/relay/consumer/crypto/customer-ordering env (read-only, never dials Kafka)"
fi
# shellcheck disable=SC2207
mcp_vars=($(binary_env_of "$repo_dir/cmd/mcp/main.go"))
(( ${#mcp_vars[@]} >= 3 )) || fail "found only ${#mcp_vars[@]} env literals in cmd/mcp/main.go; the scan is looking in the wrong place"
assert_env_rendered "$mcp_dep" "the mcp Deployment" "${mcp_vars[@]}"
# The mcp Service pins component=mcp; the Kong route is opt-in and rewrites to /mcp.
mcp_all="$(tpl "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-deployment.yaml --show-only templates/mcp-service.yaml)"
[[ "$(grep -c 'kind: Service' <<<"$mcp_all")" == 1 ]] || fail "the mcp Service was not rendered with mcp.enabled=true"
if grep -q 'kind: HTTPRoute' <<<"$mcp_all"; then fail "the mcp HTTPRoute must not render while mcp.route.enabled is false"; fi
mcp_route="$(tpl "${base[@]}" --set mcp.enabled=true --set mcp.route.enabled=true --show-only templates/mcp-route.yaml)"
grep -q 'kind: HTTPRoute' <<<"$mcp_route" || fail "the mcp HTTPRoute was not rendered with mcp.route.enabled=true"
grep -q 'replacePrefixMatch: /mcp' <<<"$mcp_route" || fail "the mcp HTTPRoute must rewrite its prefix to /mcp"
grep -q 'value: /api/mcp/shipping-and-tracking' <<<"$mcp_route" || fail "the mcp HTTPRoute must listen on /api/mcp/shipping-and-tracking"

# The web remote: its own nginx Deployment + ClusterIP Service, NO env at all,
# and no route in this chart (the Nginx web gateway owns /mfes/shipping-and-tracking/).
frontend_dep="$(tpl "${base[@]}" --set frontend.enabled=true --show-only templates/frontend-deployment.yaml)"
frontend_svc="$(tpl "${base[@]}" --set frontend.enabled=true --show-only templates/frontend-service.yaml)"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_dep" || fail "the frontend Deployment was not rendered with frontend.enabled=true"
grep -q 'type: ClusterIP' <<<"$frontend_svc" || fail "the frontend Service must stay ClusterIP"
if grep -Eq 'name: [A-Z_]+$' <<<"$frontend_dep"; then fail "the frontend Deployment must render no env (static bytes, nothing configured)"; fi

# ALL optional components render TOGETHER (ct lint's values file).
together="$(tpl -f "$chart_dir/ci/all-components-values.yaml")"
for component in api mcp frontend; do
  grep -q "app.kubernetes.io/component: $component\$" <<<"$together" || fail "the full render lacks the $component component"
done
[[ "$(grep -c '^kind: Deployment' <<<"$together")" == 3 ]] || fail "the full render must carry exactly the api, mcp and frontend Deployments"
if grep -E 'kind: (Ingress|HTTPRoute)' <<<"$together" | grep -q frontend; then
  fail "frontend routing must not live in this chart"
fi

echo "PASS: shipping-and-tracking chart render assertions"
