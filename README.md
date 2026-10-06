# shortcut-dispatcher

TypeScript keyboard shortcut dispatcher for multi-key sequences, driven
exclusively by explicit clock events (no wall-clock reads, no real timers),
so behavior is fully deterministic and replayable in tests.

## Usage

```ts
import { ShortcutDispatcher } from "./src/dispatcher.ts";

const dispatcher = new ShortcutDispatcher(
  {
    timeoutMs: 500,
    contexts: {
      global: [
        { sequence: ["g"], command: "goto" },
        { sequence: ["g", "d"], command: "goto-definition" },
      ],
      modal: [{ sequence: ["g"], command: "modal-goto" }],
    },
  },
  ["global"], // context stack, topmost last
  (command, time) => console.log("fire", command, time),
);

dispatcher.keyDown("g", 0); // ambiguous: waits until t=500
dispatcher.tick(500);       // deadline reached -> fires "goto"
```

## Event API (all timestamps are explicit logical time)

- `keyDown(key, time)` — key press; settles due timeouts first.
- `tick(time)` — pure clock event; fires a pending command whose deadline
  has been reached (`deadline <= time`).
- `focusChanged(time)` — cancels the pending sequence without firing.
- `setContextStack(stack, time)` — replaces the context stack and
  re-adjudicates the already-typed keys.
- `updateConfig(config, time)` — atomically hot-reloads the config, bumps
  the generation, and re-adjudicates the already-typed keys.
- `pause()` / `resume()` — pause drops key/tick events; resume preserves
  the pending sequence, its original deadline, and its generation.
- `getState()` — snapshot for inspection.

## Semantics

- **Prefix waiting**: when a typed buffer is an exact binding *and* a prefix
  of a longer one, the short command pends until `time + timeoutMs`.
- **Exact expiry**: a key arriving exactly at the deadline first lets the
  pending short command fire, then the key is matched from scratch.
- **Mismatch**: if the buffer plus the new key matches nothing, the pending
  short command (if any) fires first, then the new key starts a fresh
  sequence.
- **Context shadowing**: contexts are consulted top-down; the first context
  with any candidate (exact or prefix) shadows all lower contexts, so an
  overlay can hide global sequences.
- **Re-adjudication**: context-stack and config changes re-resolve the
  buffered keys (keeping the original deadline) but never emit commands, so
  re-evaluation can never trigger a command twice. Buffers that no longer
  match anything are dropped silently.
- **Generation**: starts at 0 and increments on each `updateConfig`; the
  pending sequence records the generation it was adjudicated in.
  Pause/resume never changes the generation.

## Tests

Tests replay explicit-clock event scripts (keys, ticks, focus, context and
config switches, pause/resume), including events sharing one timestamp:

```sh
npm test
```

Requires a Node version with native TypeScript type stripping (>= 22.18,
developed against v24). The `test` script points at the workspace-local
Node 24 runtime; override by running
`node --test "test/**/*.test.ts"` with any suitable Node.
