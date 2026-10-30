#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

`app.kubernetes.io/name` + `instance` are identical on every pod a release
creates. A Service that selects on those two alone would select every
component. Every Deployment here carries `app.kubernetes.io/component` in
selector.matchLabels and its pod labels, and every Service selects on it, so
each Service selects EXACTLY ONE Deployment. Components: api (always), mcp
(ADR 0006, default off), a2a (ADR 0009, default off) and frontend (the nginx pod serving the shipping_mfe
remote, ADR 0007, default off), all rendered TOGETHER here. The test also
renders with every optional template on (HPA, Ingress, HTTPRoute) and fails
if a workload ever appears without its own selector.

Run: python3 charts/shipping-and-tracking/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "shipping-and-tracking"

# Dummy values (ci/default-values.yaml), nothing is ever dialled.
BASE = ["-f", str(CHART_DIR / "ci" / "default-values.yaml")]
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "mcp.enabled=true",
    "--set", "a2a.enabled=true",
    "--set", "frontend.enabled=true",
]

# The MCP and A2A servers are read-only: no Kafka, no relay, no consumers, no
# crypto, no customer-ordering.
MCP_STRAY = {
    "KAFKA_BROKERS", "EVENT_PUBLISHER", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
    "VENDOR_CONSUMER_GROUP", "LINEHAUL_CONSUMER_GROUP", "LASTMILE_CONSUMER_GROUP",
    "CATALOG_CONSUMER_GROUP", "SILENCE_SWEEP_INTERVAL", "HOUSEKEEPING_INTERVAL",
    "SHIP_TO_ENCRYPTION_KEY", "STOP_KEY_SECRET", "CUSTOMER_ORDERING_URL",
}


def render(extra_args: list[str]) -> list[dict]:
    result = subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in dep["spec"]["template"]["spec"]["containers"][0].get("env", [])]


def command_of(dep: dict) -> list[str] | None:
    return dep["spec"]["template"]["spec"]["containers"][0].get("command")


def pins(svc: dict | None, component: str) -> bool:
    return svc is not None and selector_of(svc).get("app.kubernetes.io/component") == component


def check(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    want = sorted([RELEASE] + [f"{RELEASE}-{c}" for c in ("mcp", "a2a", "frontend")])
    if sorted(deployments) != want:
        failures.append(f"expected the Deployments {want}, got {sorted(deployments)}")
    if sorted(services) != want:
        failures.append(f"expected the Services {want}, got {sorted(services)}")
    elif selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    # The MCP Deployment runs /app/mcp with its own env, none of the api's.
    mcp = deployments.get(f"{RELEASE}-mcp")
    if mcp is None:
        failures.append("the MCP Deployment was not rendered with mcp.enabled=true")
    else:
        if command_of(mcp) != ["/app/mcp"]:
            failures.append("the MCP Deployment must run /app/mcp")
        env = set(env_names(mcp))
        stray = MCP_STRAY & env
        if stray:
            failures.append(f"the MCP Deployment must not get Kafka/relay/consumer/crypto env (it is read-only and never dials Kafka): {sorted(stray)}")
        for need in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL", "MCP_ADDR", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT"):
            if need not in env:
                failures.append(f"the MCP Deployment does not render {need}")
    if not pins(services.get(f"{RELEASE}-mcp"), "mcp"):
        failures.append("the MCP Service must exist and pin component=mcp")

    # The A2A Deployment runs /app/a2a with its own env, none of the api's.
    a2a = deployments.get(f"{RELEASE}-a2a")
    if a2a is None:
        failures.append("the A2A Deployment was not rendered with a2a.enabled=true")
    else:
        if command_of(a2a) != ["/app/a2a"]:
            failures.append("the A2A Deployment must run /app/a2a")
        env = set(env_names(a2a))
        stray = MCP_STRAY & env
        if stray:
            failures.append(f"the A2A Deployment must not get Kafka/relay/consumer/crypto env (it is read-only and never dials Kafka): {sorted(stray)}")
        for need in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL", "A2A_ADDR", "A2A_AGENT_URL", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT"):
            if need not in env:
                failures.append(f"the A2A Deployment does not render {need}")
    if not pins(services.get(f"{RELEASE}-a2a"), "a2a"):
        failures.append("the A2A Service must exist and pin component=a2a")

    # The frontend Deployment serves static bytes: NO env at all.
    frontend = deployments.get(f"{RELEASE}-frontend")
    if frontend is None:
        failures.append("the frontend Deployment was not rendered with frontend.enabled=true")
    elif env_names(frontend):
        failures.append(f"the frontend Deployment must render no env (static bytes): {env_names(frontend)}")
    if not pins(services.get(f"{RELEASE}-frontend"), "frontend"):
        failures.append("the frontend Service must exist and pin component=frontend")
    elif services[f"{RELEASE}-frontend"]["spec"].get("type") != "ClusterIP":
        failures.append("the frontend Service must be ClusterIP")
    for d in docs:
        if d.get("kind") in {"Ingress", "HTTPRoute"} and "frontend" in d["metadata"]["name"]:
            failures.append(f"{d['kind']} {d['metadata']['name']}: frontend routing must not live in this chart")

    # Every Deployment's own selector pins a component and matches its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        hit = [d for d, dep in deployments.items() if matches(selector_of(svc), pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # The HPA owns replicas when enabled and scales the api Deployment.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    # The Ingress and the HTTPRoute point at the api Service.
    for d in docs:
        if d.get("kind") == "Ingress":
            for rule in d["spec"]["rules"]:
                for p in rule["http"]["paths"]:
                    if p["backend"]["service"]["name"] not in services:
                        failures.append("the Ingress backend is not a Service of this chart")
        if d.get("kind") == "HTTPRoute":
            for rule in d["spec"]["rules"]:
                for ref in rule["backendRefs"]:
                    if ref["name"] not in services:
                        failures.append("the HTTPRoute backend is not a Service of this chart")


def main() -> int:
    failures: list[str] = []
    check(render(ENABLE_EVERYTHING), failures)

    # Default values render the api Deployment and Service only: no mcp, no
    # a2a, no frontend, no HPA, no route.
    defaults = render(BASE)
    kinds = {d["kind"] for d in defaults}
    stray = sorted(kinds & {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"})
    if stray:
        failures.append(f"optional resources rendered with default values: {stray}")
    names = sorted(d["metadata"]["name"] for d in defaults if d.get("kind") in {"Deployment", "Service"})
    optional = [n for n in names if n.endswith(("-mcp", "-a2a", "-frontend"))]
    if optional:
        failures.append(f"optional components rendered with default values: {optional}")

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1
    print("PASS: every Service selects exactly one Deployment (api, mcp, a2a, frontend); the MCP and A2A "
          "Deployments run /app/mcp and /app/a2a with their own env only and the frontend Deployment renders none; "
          "mcp, a2a and frontend are off by default; HPA, Ingress and HTTPRoute are off by default and target the api")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
