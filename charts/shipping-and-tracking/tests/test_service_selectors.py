#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

`app.kubernetes.io/name` + `instance` are identical on every pod a release
creates. A Service that selects on those two alone would select every
component. Every Deployment here carries `app.kubernetes.io/component` in
selector.matchLabels and its pod labels, and every Service selects on it, so
each Service selects EXACTLY ONE Deployment. This chart has one component, api;
the test also renders with every optional template on (HPA, Ingress, HTTPRoute)
and fails if a second workload ever appears without its own selector.

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
]


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


def check(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE}:
        failures.append(f"expected exactly the api Deployment {RELEASE!r}, got {sorted(deployments)}")
    if set(services) != {RELEASE}:
        failures.append(f"expected exactly the api Service {RELEASE!r}, got {sorted(services)}")
    elif selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

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

    # Default values render the api Deployment and Service only.
    kinds = {d["kind"] for d in render(BASE)}
    stray = sorted(kinds & {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"})
    if stray:
        failures.append(f"optional resources rendered with default values: {stray}")

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1
    print("PASS: the api Service selects exactly one Deployment (component=api); the Deployment selector pins "
          "the component; HPA, Ingress and HTTPRoute are off by default and target the api")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
