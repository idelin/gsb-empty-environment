# Preemptive Single-Machine Scheduler

Go solver for single-machine scheduling with preemption, release times,
integer processing amounts, and deadlines, minimizing total weighted
tardiness over a horizon of at most 20 time slots.

## Model

- Time is slotted `0..horizon-1`; at most one task runs per slot; idle
  slots (`-1`) are always allowed.
- Task `j` is available in slots `t >= release` and needs `processing`
  (integer) slots, not necessarily consecutive (preemption allowed).
- Completion time `C_j` is the right end of the last processed slot,
  i.e. `(last slot index) + 1`.
- Tardiness `T_j = max(0, C_j - deadline)`; the objective minimizes the
  sum of `weight * T_j`.
- A task with `processing == 0` needs no slot; it is complete at its
  release time, contributing `weight * max(0, release - deadline)`.
- A task still incomplete at the horizon is penalized by a large constant
  plus `weight * max(0, horizon - deadline)`, so completable tasks are
  always finished when feasible.
- Ties are broken by the lexicographically smallest per-slot task-id
  sequence, with idle (`-1`) ordering before any task id.

## Method

Exhaustive state-space search (DFS over slot index and the
remaining-processing vector) with memoization. Correctness — including
idle slots and zero-processing tasks — is cross-checked against a
non-memoized brute-force enumerator in `solver_test.go`.

## Usage

```sh
go build -o scheduler .
echo '{"horizon":4,"tasks":[{"id":1,"release":0,"processing":3,"deadline":100,"weight":1},{"id":2,"release":1,"processing":1,"deadline":2,"weight":10}]}' | ./scheduler
# {"cost":0,"schedule":[1,2,1,1],"completions":{"1":4,"2":2}}
```

Input (JSON on stdin): `horizon` (0..20) and `tasks` with `id`, `release`,
`processing`, `deadline`, `weight`. Output: minimum `cost`, the optimal
per-slot `schedule` (`-1` = idle), and per-task `completions`.

## Test

```sh
go test ./...
```
