# Multi-node Hybrid Logical Clock (Python 3.11, stdlib only)

A from-scratch implementation of a distributed hybrid logical clock (HLC)
whose labels carry a **physical** component, a **logical** component and a
**node epoch**, together with a durable high-water mark, crash injection
around the durability window and an independent cross-node causal-trajectory
validator.

## Time model (`hlc/time_model.py`)

Three notions are deliberately distinct, so they can never be silently mixed:

- `WallReading` — the raw hardware counter; meaningless before calibration.
- `Instant` — a point on the calibrated synthetic timeline (microseconds).
- `Duration` — a signed distance between two instants (microseconds).

Each node converts raw readings to instants through a piecewise constant
**synthetic offset conversion table** (`OffsetTable`). All arithmetic is
integer-based, so time is represented exactly (no floats). The hardware
counter only moves forward; an observed wall-clock step or **rollback** is
modelled by replacing the offset table — exactly what NTP does to a node.

## Labels and ordering (`hlc/tag.py`, `hlc/clock.py`)

Wire form: `hlc/<physical>.<logical>.<epoch>/<node>` (hex components).

Global ordering:

```
(physical, epoch, logical, node)
```

- Same-millisecond events strictly increment `logical`.
- On receive, the merged local label is strictly greater than the remote
  label, even when the remote is from a leading node.
- When the bounded logical counter overflows, the physical component is
  advanced by one explicit microsecond "leap" instead of wrapping.
- `epoch` is the node incarnation: bumped once per recovered restart. Putting
  epoch ahead of logical guarantees that any post-restart label dominates
  same-physical labels from the dead incarnation, so a published label can
  never be reused after restart.

## Durability protocol (`hlc/persistence.py`, `hlc/node.py`)

The durable log is an append-only SQLite table (`synchronous=FULL`, WAL).
Every label is durably committed **before** it is externally published:

1. tick HLC → stage `tag` (+`send`/`seen`) in one transaction;
2. fsync/commit (the label is now publication-safe);
3. publish the side effect into the network;
4. fsync the `ack` that clears the outbox entry.

Injected crash points:

- `BEFORE_SYNC` — rollback, nothing survives; restart runs a new epoch and
  does not reuse the unpublished label.
- `AFTER_SYNC` — label durable but unpublished; the recovered outbox
  republishes the *same* label; peers dedupe on `msg_id`.
- `AFTER_PUBLISH` — ack lost; restart republishes; peers use their durable
  `seen` set to drop the duplicate.

A far-future remote label whose physical component exceeds
`max(wall, local physical) + allowed_skew` is **rejected into quarantine**
without touching clock state, so one hostile label cannot make subsequent
timing run away.

## Validator (`hlc/validator.py`)

`CausalValidator` consumes only the public trace and rebuilds the
happens-before graph: local edges between successive published labels on each
node, plus the `send → receive` edge for every consumed message. It asserts
that **every** causal edge is strictly increasing, labels are never reused,
epochs never regress, logical counters stay bounded, the graph is acyclic and
quarantined messages never become edges.

## Replayable sample (`hlc/scenarios.py`, `hlc/replayer.py`, `bin/replay_demo.py`)

`MIXED_CLOCK_SCENARIO` is a plain-data operation log covering same-millisecond
bursts, a wall-clock rollback, remote-label merge, logical overflow, a
far-future attack label, all three crash windows with behind-wall restarts and
duplicate redelivery. Replaying it is deterministic: two runs into fresh
directories produce byte-identical JSON.

## Run

```bash
python3.11 -m unittest discover -s tests -v
python3.11 bin/replay_demo.py
```

The canonical trace is written to `artifacts/mixed_clock_trace.json`.

## Layout

```
hlc/time_model.py   wall readings / instants / durations / synthetic clock
hlc/tag.py          HLC label, ordering, wire codec
hlc/clock.py        HLC state machine, overflow policy, far-future test
hlc/persistence.py  SQLite durable log with crash injection + recovery
hlc/node.py         node: clock + durable high-water mark + outbox/inbox
hlc/network.py      deterministic inboxes, redelivery, hostile injection
hlc/trace.py        observable event stream
hlc/validator.py    independent causal-trajectory validator
hlc/scenarios.py    the replayable mixed-clock scenario script
hlc/replayer.py     deterministic replayer + JSON artifact
tests/              unittest suite (time model, HLC, full scenario)
```
