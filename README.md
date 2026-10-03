# Deterministic Event-Replay Debugging Kernel

A side-effect-free, time-traveling event scheduler in TypeScript. Every
external stimulus — network packet arrivals, clock-interrupt timeouts and
random-seed injections — is recorded into an ordered event log. Any
historical event tick can be revisited by replaying the log prefix onto a
pristine kernel, reproducing every component's memory invariants
byte-for-byte.

## Design

- `src/kernel.ts` — scheduler + recorder + time travel. Events are
  dispatched in total `(tick, seq)` order; after each event a canonical
  snapshot of all component state (clock, RNG, network stack, timers) is
  captured. `travelTo(n)` resets all components in memory and re-dispatches
  `log[0..n)`; `resume()` continues forward from the traveled point.
- `src/events.ts` — event log types: `packet`, `timeout`, `reseed`.
- `src/components.ts` — simulated `NetworkStack` (rx queue, congestion
  window, deterministic loss) and `TimerService`; all state is plain
  serializable data, no I/O, no hidden references.
- `src/rng.ts` — mulberry32 PRNG whose entire state is one uint32, captured
  in every snapshot; reseed injections are recorded as log events.
- `src/serialize.ts` — canonical serialization (recursively sorted keys) +
  SHA-256. Byte-identical canonical bytes are the replay comparison target.
- `src/scenario.ts` — the acceptance trace: 50 packet arrivals, 3 clock
  timeouts, 2 seed injections (55 events), generated deterministically.

## Requirements

Node.js >= 23.6 (runs TypeScript natively via type stripping; zero
dependencies, zero build step).

## Run

```sh
npm test     # node --test test/*.test.ts
npm sample   # npm run sample — records the trace, time-travels to event 35
```

## Acceptance

The recorded trace contains exactly 50 `packet` and 3 `timeout` events.
Replaying to the 35th event yields a state whose canonical bytes and
SHA-256 hash are identical to the snapshot recorded at run time; the test
suite verifies this for all 55 historical ticks, plus backward travel +
resume, cross-run determinism, and tamper detection (the log is the single
source of truth).
