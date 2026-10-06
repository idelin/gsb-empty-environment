# Two-Machine Deadlock Checker

Two finite-state machines communicate over two directional FIFO queues,
each of capacity 1 (A→B and B→A). Transitions are `send(m)`, `recv(m)`,
or internal `tau`. The checker exhaustively explores the finite product
state space with BFS and reports a shortest trace to a deadlock: a
non-terminal global state in which neither machine has an enabled action.

Semantics:

- `send(m)` is enabled iff the sender's outgoing queue has free capacity.
- `recv(m)` is enabled iff the incoming queue's head message equals `m`.
- `tau` is always enabled.
- A machine state with no outgoing transitions is terminal; a global state
  is terminal iff both machines are terminal (clean termination, not a
  deadlock — even if messages remain queued).
- No fairness assumption: every enabled interleaving is explored.

`System.Replay` independently re-executes a trace and verifies each step:
the actor is in the transition's `From` state, the transition is declared,
the action is legal against current queue contents, and the recorded
post-state (including queues) matches the recomputed one.
`System.VerifyDeadlockTrace` additionally checks the final state is a
genuine deadlock.

Run:

    GO111MODULE=on go run .        # demo: mismatch protocol + ping-pong
    GO111MODULE=on go test ./checker/
