# completion-range-mapper

A small TypeScript library for language-completion sessions whose responses
arrive against a **stale document version**. It maps replacement ranges
forward through the edits that happened since, decides whether the response
is still applicable, and applies an accepted completion — across multiple
cursors — as **one undoable transaction**.

Requires Node.js ≥ 22.18 (runs TypeScript natively via type stripping; no
build step). Run the tests with:

```sh
npm test
```

## Core ideas

### Versions and the edit chain

Every document mutation is recorded in an append-only `EditChain`
(`src/chain.ts`); each edit moves the version forward by one. A completion
response is anchored at the version it was computed against, and the chain
is the source of truth for reconciling it with the current document:

```ts
const editor = new TransactionalEditor("foo");
const session = new CompletionSession(editor, { version: 0, items });

editor.document.insert(3, "b"); // user keeps typing: v1
session.items();              // ranges mapped v0 -> v1
```

Undo and redo are themselves recorded as ordinary edits, so versions never
go backwards and old responses remain verifiable against the chain.

### Range mapping, invalidation, affinity

`mapRangeThroughEdit` (`src/mapping.ts`) maps a range through a single edit:

- **Interior edits invalidate.** A deletion/replacement whose removed span
  intersects the range content, or an insertion strictly inside the range,
  makes the mapping `{ valid: false }` — the completion is stale.
- **Boundary insertions map by affinity.** An insertion exactly at a
  boundary never invalidates; each boundary has an affinity
  (`src/types.ts`):
  - `"after"` — the boundary moves past the inserted text (at the range
    end, this *absorbs* continued typing into the range);
  - `"before"` — the boundary stays in front of the inserted text.

  The default for completions is `{ start: "after", end: "after" }`, so
  typing at the cursor grows the replacement range while typing at the
  range start stays outside.

### Accepting a completion

`CompletionSession.accept(item, cursors?)` (`src/completion.ts`):

1. Checks the response version is inside the tracked window
   (`StaleCompletionError` otherwise).
2. Maps the item's range — and every secondary cursor range — from the
   response version to the current version through the edit chain. If any
   range was invalidated, nothing is applied.
3. Applies all replacements as a **single undoable transaction**
   (`TransactionalEditor.transact`, `src/editor.ts`): one `undo()` reverts
   the whole accept, across all cursors.

```ts
const tx = session.accept(item, [secondaryCursorRange]);
editor.undo(); // every cursor's replacement reverted together
```

## Layout

- `src/types.ts` — `Range`, `TextEdit`, affinity types
- `src/mapping.ts` — single-edit offset/range mapping + invalidation rules
- `src/chain.ts` — `EditChain` (version edit log) and `VersionedDocument`
- `src/editor.ts` — `TransactionalEditor` with transaction undo/redo
- `src/completion.ts` — `CompletionSession`, `StaleCompletionError`

Ranges are half-open character-offset spans `[start, end)`; converting
to/from line/character positions is left to the host.
