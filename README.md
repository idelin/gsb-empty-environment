# Perishable lot-sizing solver (Go)

Two exact solvers for a perishable-inventory production planning problem,
plus brute-force verification harnesses.

## Model

- Discrete horizon (demo: 10 days), integer quantities, total demand <= 30.
- Shelf life `d` days: a unit produced on day `t` is usable on days
  `t .. t+d-1`; leftovers expire at the end of their last usable day.
- Daily production capacity `Cap`, fixed setup cost `Setup` per production
  day, unit cost `Unit`, holding cost `Hold` per unit carried to the next day.
- Consumption is FEFO (earliest-expiry-first = oldest age first).
- No stockouts: every day's demand must be covered from stock + production.

Inventory is tracked as an age vector `inv[a-1]` = units of age `a`
(`a = 1..d-1`) at the start of a day; fresh production has age 0.

## 1. Deterministic (`deterministic.go`)

Exact DP over `(day, age-vector)`. `solveDeterministic` returns the minimal
setup + production + holding cost and the production plan; `buildLedger`
replays the plan into a per-day batch age account (start inventory by age,
production, demand, consumption by age, expired units, end inventory).

## 2. Stochastic scenario tree (`stochastic.go`)

- Demand of day `t` is revealed at the start of day `t`, then the production
  decision is made. Up to 6 scenarios with rational probabilities.
- Non-anticipativity: scenarios sharing the same observed demand history
  share a tree node and therefore the same decision and the same inventory
  age vector (shared perishable stock account).
- `solveStochastic` computes the minimal expected cost exactly with
  `math/big.Rat` and stores the optimal policy tree; no path may stock out.

## Verification (`verify.go`, `main_test.go`)

- Age-vector simulator cross-checked against an independent batch-list
  simulator on thousands of random days.
- Expiry boundary: hand-built cases pin down the semantics "produced on day
  `t` is usable through day `t+d-1`, expires afterwards" (incl. `d = 1`).
- Deterministic DP vs exhaustive enumeration of all production sequences.
- Stochastic tree DP vs exhaustive enumeration of all non-anticipative
  policies; extracted policy checked for non-anticipativity and for batch
  ledger consistency along every scenario path.

## Run

```sh
go run .     # demos + verification summary
go test ./...# verification as unit tests
```
