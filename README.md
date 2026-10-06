# Two-Machine Deadlock Checker

Two finite state machines communicate over bidirectional message queues of
capacity one (one queue per direction). Transitions are `send`, `recv`, or
`internal` actions.

- `checker/model.go` — model: composite state `(locA, locB, qAB, qBA)`,
  enabledness (`send` needs queue space, `recv` needs matching head message),
  transition application, terminal vs. deadlock definition.
- `checker/explore.go` — `FindDeadlock` does BFS over the finite composite
  state space and returns a shortest trace to a non-terminal state with no
  enabled action. Fairness is not assumed: every enabled transition of either
  machine is explored, so any deadlock reachable under some schedule is found.
- `checker/explore.go` — `VerifyTrace` / `CheckDeadlockTrace` replay a trace
  step by step, checking machine id, edge existence, enabledness (queue
  contents and capacity), and that the final state is a genuine deadlock.

Run:

    go test ./...
    go run .
