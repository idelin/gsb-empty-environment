# schedenum — cooperative scheduling enumerator

Enumerates every distinct schedule of up to 4 cooperating coroutines
within a bounded number of steps, and prints the shortest event sequence
that violates an assertion.

## Model

- A coroutine is a fixed list of operations (`Thread.Ops`).
- Coroutines yield **only** at explicit yield points: `ReadOp`,
  `WriteOp`, `WaitOp`, `AssertOp`. `LocalOp` never yields; consecutive
  local ops collapse into the surrounding step.
- `WaitOp` blocks the coroutine (its pc stays on the wait op) until its
  condition holds; blocked coroutines are not schedulable.

## Merging

Duplicate branches are merged on a key containing the shared memory
**and**, per coroutine, the program counter, local registers and the
wait tag. Two branches with identical memory but different wait states
(hence different runnability) are therefore never merged.

## Search

Breadth-first over merged states, so the first assertion violation
found is a shortest one. `Result` also reports the shortest deadlocked
schedule, the number of distinct states/transitions, and whether the
step bound truncated live schedules.

## Usage

```go
ex := &Explorer{
    InitMem:  Mem{"x": 0},
    MaxSteps: 32,
    Threads: []Thread{
        {Name: "T1", Ops: []Op{
            ReadOp("x", "t"),
            WriteOp("x", func(l Locals) int { return l["t"] + 1 }),
        }},
        // ... up to 4 threads
    },
}
res := ex.Explore()
```

Run the demos and tests:

```sh
go run .    # lost-update demo prints a 10-event shortest violation
go test ./...
```
