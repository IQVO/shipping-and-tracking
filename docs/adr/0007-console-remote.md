# ADR 0007: the console remote (shipping_mfe) — a read-only screen over the existing REST

## Status

Accepted (2026-10-24).

## Context

ADR 0001 scoped this context as a backend tier with no user-facing screen of
its own: a `web/` directory would be drift until an ADR records why a remote
is needed and who consumes it. That decision is now taken: operators and
support walk "where is this package, what has it been through" through the
warehouse-console shell like every other context, and the shell mounts
per-context Module Federation remotes. This ADR is that record.

## Decision

### The remote

`web/` is a Vite + React Module Federation remote, container `shipping_mfe`,
exposing `./App` only (no props, relative routes), built on the fleet's
reference recipe (task-interleaving's web remote) rather than re-derived:

- PINNED host/remote contract: federation container `shipping_mfe`, gateway
  mount `/mfes/shipping-and-tracking/`, dev/preview port **4020** —
  deliberately off the console's 5181–5197 ladder, whose every slot an
  existing remote holds, and clear of the 4010–4016 range other read-only
  remotes took; 4020 collides with no remote and no service port.
- The API base is resolved at RUNTIME from the console's `/config.json`
  (`window.__WAREHOUSE_CONFIG__.apiOrigin` →
  `${apiOrigin}/api/shipping-and-tracking`); a production build throws when
  it is missing instead of falling back to a dev port. The remote calls ONLY
  its own context's API, and only its GETs.
- Packaged as `nginxinc/nginx-unprivileged` (`web/Dockerfile`,
  `web/nginx.conf`), served under `/mfes/shipping-and-tracking/` by
  warehouse-infra's Nginx web gateway. Kong never serves these bytes; no
  ingress/httproute; the chart Service stays ClusterIP.
- `@warehouse/ui-kit` is a sibling checkout consumed as a named BuildKit
  context; the image never installs from a foreign `node_modules`.

### The screens — existing REST only, no new endpoints

Three surfaces, exactly what `apis/openapi.yaml` serves today:

1. **Journeys** — `GET /journeys/{trackingNumber}` behind a lookup by
   tracking number (the one identifier a shopper or an operator holds; the
   contract has no list endpoint).
2. **Journey detail** — the same GET: the state, the label's facts (never an
   address: the contract keeps the ship-to out of every response), and every
   stored scan, applied or not, in the order they were recorded.
3. **Scan reference** — the fixed scan-type edge table of the contract (ADR
   0002 A5), static, so an operator reading a journey can tell an applied
   scan from a stored-only one without leaving the console.

Where a screen wanted data the API does not serve, the screen was cut, not
the API. The remote is READ-ONLY by construction: it issues no POST, so it
can never mint an `Idempotency-Key`. Issuing and voiding labels and recording
scans stay with vendor-connectivity and the API.

### The deployment surface

The Helm chart grows a `frontend` component (`frontend-deployment.yaml`,
`frontend-service.yaml`, `_frontend.tpl`), default **off**; the Service
selects `component=frontend` so no Service ever selects two Deployments, and
the frontend Deployment renders NO env — nothing about the remote is
configured in the cluster. CI grows a `web` job (ui-kit build, npm ci, oxlint,
tsc, vitest, production build). Registering the remote in the console shell
(its `vite.config.ts` entry, route, nav) is the console repo's change, not
this repo's.

## Consequences

- The "no frontend" stance of ADR 0001's scope is superseded by this ADR: a
  `web/` directory and a frontend CI job are now in scope for this repo.
- Operators get the package timeline in the console; the API surface is
  unchanged (openapi.yaml untouched — the docs-api-drift job stays green by
  construction).
- The dev port 4020 must stay unique across the fleet's remotes; any
  collision is fixed HERE, in the remote's repo.
