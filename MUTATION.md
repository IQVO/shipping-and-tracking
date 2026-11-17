# Mutation testing: known equivalent mutants

Gate: `.gremlins.yaml` (`efficacy: 97`, `mutant-coverage: 99`), run per
package by `make mutation` (CI `mutation-fast`: every `internal/domain/*`
package plus `internal/analytics/report`) and on `./internal/domain` by
`make mutation-full` (CI `mutation`). gremlins v0.6.0.

Measured 2026-11-15, after the ratchet tests: 0 not covered everywhere; the only
lived mutants are the two below, both in `journey`.

| Package | File:line | Mutator | Why it is equivalent |
|---|---|---|---|
| `internal/domain/journey` | `journey_carrier.go:33` | `CONDITIONALS_BOUNDARY` (`<` -> `<=`) | `x.CostPerPackageCents < y.CostPerPackageCents` is only reached inside `if x.CostPerPackageCents != y.CostPerPackageCents`, so the two costs are never equal there and `<` and `<=` return the same value. |
| `internal/domain/journey` | `journey_carrier.go:36` | `CONDITIONALS_BOUNDARY` (`>` -> `>=`) | Same shape: `x.TransitMinutes > y.TransitMinutes` is only reached inside `if x.TransitMinutes != y.TransitMinutes`. |

Effect on the gate: journey measures 88 killed / 2 lived = 97.78% efficacy,
so `efficacy` is 97 (strictly below it). Reaching 99 would need production code
changed to drop the redundant guard (or to compare differently), which a
test-only ratchet does not do. Every other package measures 100% / 100%.
