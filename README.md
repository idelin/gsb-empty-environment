# Ticket Routing Analyzer

Analyzes a ticket routing spec with **finite field domains** and answers two
questions for **every** possible input ticket:

1. **Termination** — does execution always reach a state where no rule
   matches, for every legal choice of rules?
2. **Unique final queue** — do all legal executions from the same input end
   in the same queue?

When a violation is found, the analyzer prints a **minimal witness**: the
smallest input state plus the exact rule-application path that demonstrates
the loop or the divergence.

## Why not static cycle checks?

The analyzer does **not** look for static reference cycles between rules
(rule A writes a field that rule B reads). Such cycles are neither necessary
nor sufficient for non-termination:

- `examples/static-cycle-but-safe.json` has a static cycle
  (`send-to-review` → `retry-intake` → `send-to-review`) yet always
  terminates, because the `attempt` field bounds the round trips.
- Conversely, a loop can depend on specific value combinations that no
  static view captures.

Instead the analyzer **enumerates the full state space** (the product of all
finite field domains), builds the exact transition graph, and model-checks
it. Every state is a legal input, so the results are exact, not
approximations.

## Spec format

```json
{
  "queue_field": "queue",
  "fields": [
    {"name": "queue", "values": ["intake", "billing", "tech"]},
    {"name": "topic", "values": ["invoice", "outage"]},
    {"name": "retries", "values": [0, 1, 2]}
  ],
  "rules": [
    {"name": "route-billing",
     "when": {"queue": "intake", "topic": ["invoice"]},
     "set": {"queue": "billing", "retries": 0}}
  ]
}
```

- `queue_field` (optional, default `"queue"`): which field holds the queue.
- `fields`: every field with its finite list of legal values (strings,
  numbers, or booleans).
- `rules[].when`: field conditions; a scalar matches that value, a list
  matches any of the listed values. All conditions must hold.
- `rules[].set`: field updates applied atomically when the rule fires.

**Execution semantics:** at each step, any matching rule may fire
(non-deterministic choice); firing applies its `set` updates. Execution
stops when no rule matches; the final queue is the queue field's value in
that terminal state. The analyzer requires termination and uniqueness under
*all* legal choices.

## Usage

```sh
go build -o ticket-analyzer .
./ticket-analyzer examples/safe.json        # human-readable report
./ticket-analyzer -json examples/loop.json  # machine-readable report
cat spec.json | ./ticket-analyzer -         # read from stdin
```

Exit codes: `0` = safe (terminates + unique), `1` = a violation was found,
`2` = usage/spec error.

Flags:

- `-json` — emit the report as JSON (includes witness inputs and paths).
- `-max-states N` — safety cap on the enumerated state space
  (default 5,000,000).

## How the analysis works

1. **Enumerate** all states (product of the finite domains) and compute the
   transition for every matching rule.
2. **Termination:** find strongly connected components; any cyclic SCC is a
   reachable infinite execution. The witness is the **shortest cycle** in
   the graph (fewest rule applications), found by BFS from cyclic nodes —
   its start state is the minimal input.
3. **Uniqueness:** propagate the set of reachable terminal queues backwards
   over the graph (least fixpoint). Any state reaching terminals with two
   different queues is a divergence. The witness minimizes the combined
   length of the two paths to distinct final queues, reconstructed via
   reverse BFS distances from the terminals.

## Limits

The method is exact but exponential in the number of fields: the state
space is the product of the domain sizes. It is intended for routing specs
whose fields are genuinely finite and modest (queues, priorities, small
counters, enums). The `-max-states` cap prevents accidental blow-ups.

## Development

```sh
go test ./...
```
