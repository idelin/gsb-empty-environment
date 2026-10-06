# scheduler

Exact solver for single-machine preemptive scheduling with weighted tardiness.

## Model

- Time is slotted: slot `t` covers `[t, t+1)`, horizon is at most 20 slots.
- Each job has an integer release time, integer processing amount, deadline,
  and weight. At most one job runs per slot; preemption is free.
- A job may run in slot `t` only if `t >= release`.
- Completion time is the right end of the last slot in which the job runs.
  A zero-processing job completes at its release time.
- Objective: minimize total weighted tardiness `sum w_i * max(0, C_i - d_i)`.
- Ties are broken by the lexicographically smallest per-slot job id
  sequence (smaller id first; an idle slot sorts after every job id).

## Usage

Reads a JSON instance from stdin (or a file argument), prints the optimal
result as JSON. Idle slots are `-1` in the schedule.

```sh
go build -o scheduler .
echo '{"slots": 4, "jobs": [{"id": 1, "release": 0, "processing": 2, "deadline": 3, "weight": 5}]}' | ./scheduler
./scheduler -verify instance.json   # cross-check against exhaustive enumeration
```

## Method

`solver.go` is an exact DP over (time, remaining-work) states with
memoization; the lexicographic tie-break falls out of visiting actions in
preference order. `brute.go` enumerates every valid per-slot assignment
without memoization and scores each complete schedule directly — it exists
to cross-check the DP (`-verify`, and `TestCrossCheckRandom`, which covers
idle slots and zero-processing jobs on randomized small instances).

```sh
go test ./...
```
