# breakpoint-anchor

A zero-dependency breakpoint library for TypeScript/JavaScript sources.
Breakpoints anchor to **statements** through an explicit statement-identity
model, not to raw line/column numbers, so they survive edit chains sanely.

## Core semantics

A breakpoint binds to the innermost statement at its position and stores the
statement's stable id plus an offset delta. On every edit the source is
re-parsed and statement ids are transferred to the new version:

| Operation | Behavior |
| --- | --- |
| Lines inserted/removed elsewhere | Breakpoint shifts with its statement, same id. |
| Statement edited in place | Identity kept via per-edit-region pairing; breakpoint stays bound. |
| Statement moved (cut + paste, or one batch edit) | Content key carries the id; the breakpoint **follows** the statement. |
| Statement deleted | Breakpoint becomes **unbound** — it never slides onto an unrelated line. Its last known position is kept for display only. |
| Delete then restore (undo / revert / paste-back) | The deleted id is revived from a bounded tombstone buffer; the breakpoint **rebinds**. |
| Statement copied | The copy is a **new** statement id; the breakpoint stays on the original. Deleting the copy does not affect it. |
| Different file revision (`setSource`) | Identity recovered by content keys — breakpoints map onto the rearranged revision. |

Every version snapshots the statement table (bounded by `historyLimit`), so
any breakpoint can be mapped to any retained version via
`mapBreakpointToVersion`.

## Identity model

`IdentityModel.matchStatements(prev, next, edits)` transfers ids in four
passes:

1. **Survivors** — statements no edit touched keep their id (matched by
   shifted position + content key).
2. **Content pairing** — remaining statements pair by content key
   (`kind` + normalized token stream), in document order. Carries ids across
   moves and reformatting.
3. **Region pairing** — within each edit region, remaining prev/next
   statements pair in document order. Keeps identity when a statement is
   edited in place (its key changed).
4. **Tombstones** — unmatched new statements may revive recently deleted
   ids with the same key (cut/paste, undo). Leftover prev statements are
   deleted and become tombstones themselves; leftover next statements get
   fresh ids.

The model never transfers an id to a statement at an unrelated location:
deletion unbinds, it does not re-anchor.

## Usage

```js
import { BreakpointStore } from 'breakpoint-anchor';

const store = new BreakpointStore('const a = 1;\nconst b = 2;\n');
const bp = store.setBreakpoint(2, 3);        // bind on line 2

store.applyEdit({ start: 0, end: 0, text: '// hi\n' });
store.getBreakpoints();                       // -> bound at line 3, same id

store.applyEdit(/* delete line 3 */);         // statement removed
store.getBreakpoints();                       // -> status: 'unbound', line: null

store.mapBreakpointToVersion(bp.id, 0);       // -> { status: 'bound', line: 2, ... }
```

Positions are 1-based `{ line, column }`; edits use 0-based half-open
offsets. `applyEdits(edits[])` treats a batch as a single version step, so a
coordinated delete+insert (a move) keeps identity atomically.

## Notes and limits

- The built-in parser is heuristic (block structure, semicolons, common
  ASI), not a full ECMAScript grammar. Statement spans and keys are stable
  for typical code; pathological syntax may split differently.
- Content keys ignore whitespace and comments, so reformatting preserves
  identity; renaming an identifier keeps identity through region pairing.
- Tombstones are bounded (`tombstoneLimit`, default 256) and evicted oldest
  first. History is bounded (`historyLimit`, default 64 versions).

## Development

```sh
npm test   # node test/run.js — no dependencies required
```
