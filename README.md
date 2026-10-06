# rollbackplanner

Config-version rollback planner for a small cluster of nodes (at most 5).
Versions form a directed acyclic dependency graph; rollback edges move a
node from a version back to one of its dependencies, undoing part of the
feature set and consuming a shared maintenance budget.

## Model

- **Version**: an id plus the set of features it provides.
- **Rollback edge** `from -> to`: moves a node back to a dependency,
  consumes `cost` units of the shared maintenance budget, and undoes the
  listed features.
- **Node**: pinned to a current version; `forbidden` lists versions the
  node may never pass through because of irreversible operations.
- **Compatibility rule**: for a pair of nodes, the exhaustive list of
  allowed version pairs; pairs without a rule are unconstrained.
- **Budget**: one shared maintenance budget consumed by every rollback
  edge taken by any node.

See `example.json` for a full input document.

## Part 1 — common rollback target (`rollback.go`)

Every node reports its applied version. The solver enumerates **all**
candidate versions reachable from every node through rollback edges
(a node can also stay put) and picks the target that

1. maximizes the number of retained features, then
2. minimizes the total rollback steps summed over all nodes,
3. (tie-break) minimizes total budget cost, then version id.

Per-node paths use the lexicographically smallest (steps, cost) route.
`VerifyPlan` independently re-checks the result from scratch: DFS
reachability of the target from every node, structural validity of each
path (real edges, correct endpoints, recomputed steps/cost), the
retained feature set, and optimality via a brute-force re-enumeration
of all candidates with independent BFS hop counts.

## Part 2 — constrained transition trajectory (`search.go`)

The plan is executed one node move at a time, and every intermediate
joint state must stay legal. `SearchTrajectory` runs an exact
state-space search over joint version vectors (at most 5 nodes):

- moves: pick one node and follow one rollback edge;
- a move is rejected if it enters a forbidden version for that node,
  pushes the cumulative cost above the shared budget, or leads to a
  state violating any compatibility rule;
- because the budget is a hard constraint, each state keeps its whole
  Pareto frontier of (steps, cost) labels — a label with more steps but
  lower cost is not dominated and may be the only feasible one;
- labels are popped in lexicographic (steps, cost) order, so the first
  goal label popped is the legal trajectory with the fewest transitions,
  then the lowest budget consumption.

`VerifyTrajectory` independently replays the trajectory and checks every
invariant: real edges with correct costs, no forbidden versions,
compatibility in every state including endpoints, budget never exceeded,
and correct initial/goal states.

## Usage

```sh
go build -o rollbackplanner .
./rollbackplanner                      # built-in example, both parts
./rollbackplanner -input scenario.json # custom scenario
./rollbackplanner -part 1              # only target selection
./rollbackplanner -part 2              # only trajectory search
go test ./...                          # solver, verifier and search tests
```
