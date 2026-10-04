# cancellable-context

Cancellable contexts with nested, adjustable deadlines, written in TypeScript.

## Semantics

- Every context may have its own deadline; the **effective deadline** is
  `min(own, parent effective)` — an operation can never outlive its parent.
- `setDeadline(at)` adjusts the own deadline at runtime:
  - **Shortening** propagates immediately: descendants recompute their
    effective deadline and cancel right away if it is already in the past.
  - **Extending** never exceeds the parent's effective deadline; the
    effective deadline stays bounded by the ancestor chain.
- `detach()` removes a child from its parent: later parent cancellations
  and deadline changes no longer affect it, and its own (unclamped)
  deadline applies from then on.
- Cancellation is **terminal**: a cancelled context ignores `setDeadline`,
  `clearDeadline`, `detach` and further `cancel` calls, and keeps its
  original reason.
- Every cancellation produces a **unique `CancellationReason`** (unique `id`,
  message, and `cause` chain linking to the parent's reason).
- `onCancel(cb)` registered on an already-cancelled context (e.g. a deadline
  that expired before or during registration) fires immediately with the
  stored reason.

## API

```ts
import { SystemClock } from "./src/clock.ts";
import { background, withCancel, withDeadline, withTimeout } from "./src/context.ts";

const root = background(new SystemClock());
const op = withTimeout(root, 5000);        // child with 5s deadline
op.onCancel((reason) => console.log(reason.toString()));
op.setDeadline(Date.now() + 1000);         // shorten: propagates at once
const reason = await op.done();            // unique CancellationReason
```

## Clocks

- `SystemClock` — real time (`Date.now` + `setTimeout`).
- `VirtualClock` — deterministic manual clock for tests (`advanceTo` /
  `advanceBy`), fires timers in chronological order.

## Tests

Requires Node.js >= 22.6 (native TypeScript type stripping):

```sh
npm test
```

The suite covers nested deadlines, parent deadline modification, child
detachment, expiry during callback registration, and uniqueness of
cancellation reasons — all on a `VirtualClock`.
