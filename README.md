# observable-notifier

An observable value with round-based listener notification. Listeners may
freely mutate the observed value from inside callbacks (reentrancy-safe).

## Semantics

- **Rounds with fixed snapshots** — each notification round iterates a
  snapshot of the listeners registered when the round began.
- **Ordering** — listeners run by `priority` (higher first), ties broken by
  registration order.
- **Merged nested updates** — calling `set` from inside a listener never
  starts a round mid-round; the update is merged and delivered in the next
  round, which uses a fresh snapshot (listeners added mid-round join the
  next round, never the current one).
- **Immediate unsubscribe** — the returned `unsubscribe()` takes effect at
  once: a listener removed before its turn in the current snapshot is
  skipped.
- **Settle detection** — if the value returns to the value listeners were
  last notified about, the pending round is cancelled; `set` with an
  `Object.is`-equal value is a no-op.
- **Exception collection** — a throwing listener never stops the round; all
  exceptions are collected and, once the value settles, `set` throws an
  `AggregateError` containing them.

## Usage

```ts
import { ObservableValue } from "./src/observable-value";

const count = new ObservableValue(0);

const unsubscribe = count.subscribe(
  (value, previous) => console.log(`${previous} -> ${value}`),
  { priority: 10 },
);

count.set(1); // logs "0 -> 1"
unsubscribe();
```

## Develop

```sh
npm test   # type-check, compile to dist/, run the call-log test suite
```
