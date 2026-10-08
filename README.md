# Rule Engine — sandboxed AST interpreter

Tenant-submitted rule expressions are evaluated by a whitelisted AST
interpreter. There is **no** node kind for `eval`, host-function calls or
dynamic imports; any unknown node kind is rejected as `ForbiddenNode`.

## Supported nodes

`int` (signed 64-bit), `bool`, `var`, `neg`, `arith` (`add/sub/mul/div/mod`,
truncating division), `cmp` (`eq/ne/lt/le/gt/ge`), short-circuit `and/or`,
`not`, `if`, and `map`/`filter`/`fold` over finite input arrays.

## Cost model (all checks happen BEFORE the action)

- 1 step per AST node visit (loop bodies are counted per iteration)
- 1 extra step per array element read by `map`/`filter`/`fold`
- 1 space slot per element of every array created by `map`/`filter`
- per-request `budget: { steps, space }` plus a per-tenant total in-flight
  space limit shared by that tenant's concurrent requests

## Isolation

- Explicit interpretation stack (`src/interpreter.ts`): AST depth is bounded
  by heap, not the JS call stack (tested at 100k nesting).
- Evaluation yields to the event loop every `sliceSteps` work units.
- Each request has its own counters and `CancelToken`; cancelling one tenant
  never affects others.
- Space reservations are released on success, failure and cancellation
  (`finally` in `src/service.ts`), so quota is immediately reusable.

## Errors

`Overflow`, `TypeError`, `UndefinedVariable`, `StepsExhausted`,
`SpaceExhausted`, `DivisionByZero`, `ForbiddenNode`, `Cancelled`,
`InvalidInput` — each reported with an AST path such as `$/args/0`.

## Run

```sh
npm test        # node --test test/*.test.ts (Node >= 23.6, native type stripping)
```
