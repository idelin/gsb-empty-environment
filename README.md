# Sortable Hierarchical List — Shared Mouse/Keyboard State Machine

TypeScript, zero dependencies, tested with the Node.js built-in test runner
(Node ≥ 22.6; developed on v23). All core computation (tree ops, layout,
hit-testing) is pure and driven by fixtures — no browser layout engine.

## Run

```sh
npm test          # node --test  (runs test/*.test.ts via type stripping)
npm run demo      # node examples/demo.ts
```

## Architecture

- `src/tree.ts` — immutable tree ops: `applyMove` (post-removal coordinates),
  `validateMove` (rejects drops into own descendants), patches, flatten.
- `src/layout.ts` — row geometry + hit-testing derived only from the glyph
  metrics fixture; `pointForLocation` is the inverse used for parity checks.
- `src/stateMachine.ts` — the shared `DragMachine`. Preview is derived
  (`previewTree()`); the canonical tree is never mutated by a drag. Cancel
  and `terminate()` restore the full UI snapshot (focus + selection).
- `src/input/mouse.ts`, `src/input/keyboard.ts` — drivers that translate
  pointer/keys into the same logical intents, yielding identical op traces.
- `src/server.ts` — authoritative server: validates the source version and
  the target on every commit, dedupes by `opId` (idempotent retries), and
  broadcasts reorder/insert/remove to other clients.
- `src/client.ts` — canonical mirror updated only from server results and
  broadcasts; failed commits discard just the preview, so independent edits
  made afterwards survive; mid-drag broadcasts rebase or safely terminate
  the drag when the node/target disappears.

## Guarantees covered by tests

- Mouse and keyboard emit identical logical traces; equal final tree, focus
  and selection; keyboard promote == mouse drag to the same spot (same patch).
- Cancel never modifies the list and restores all UI state.
- Two competing drag sessions: first commit wins, loser converges via broadcast.
- Lost ack + retry applies exactly once (opId idempotency).
- Stale commits fail without reverting the user's later independent edits.
- Deleting the dragged node or the drop target mid-drag terminates safely.
