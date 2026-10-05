# Finite-State Optimal Planner

A STRIPS-style planner in Go. States are sets of boolean propositions
(at most 20, encoded as a `uint32` bitmask); actions have preconditions,
add/delete effects and non-negative integer costs. The solver runs
Dijkstra (uniform-cost) search and returns a minimum-cost plan.

## Problem format

```
props p1 p2 ...      # at most 20 propositions
init  p1 ...         # true in the initial state
goal  p3 ...         # must hold at the end
action <name>        # starts an action block
  pre  ...           # preconditions
  add  ...           # add effects
  del  ...           # delete effects
  cost <n>           # non-negative integer (default 0)
```

Blank lines and `#` comments are ignored.

## Usage

```
planner solve  [-plan out.txt] <problem.txt>
planner replay <problem.txt> <plan.txt>
```

- `solve` prints the minimum-cost plan, or — when the goal is
  unreachable — a summary of every explored state with its best cost.
- `replay` is an independent validator: it re-simulates the plan from
  the initial state, checks each step's preconditions, applies the
  effects, and verifies the goal at the end.

## Zero-cost cycles

A state is only re-queued when a strictly cheaper path reaches it.
Zero-cost loops yield equal (not smaller) costs and are discarded, and
non-negative integer costs bound the number of improvements per state,
so the search always terminates.

## Development

```
go test ./...
```

See `examples/robot.txt` (solvable, includes a zero-cost cycle) and
`examples/unreachable.txt`.
