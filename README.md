# observable-notify

Observable state with re-entrant, round-based listener notification.
Listeners may freely mutate the observed value, subscribe, or unsubscribe
from inside callbacks.

## Semantics

- **Ordering**: listeners run by ascending `priority`; ties break by
  registration order.
- **Fixed snapshot per round**: each round iterates a snapshot taken at
  its start. Listeners subscribed mid-round join from the next round on.
- **Immediate unsubscribe**: a listener removed before its turn in the
  current snapshot is skipped.
- **Merged nested updates**: `set` called from inside a listener never
  recurses. Nested writes coalesce and only the final value is dispatched
  in the next round.
- **Value restored**: if nested writes restore the value listeners saw
  before the current round, the round is moot and no further round runs.
- **Collected exceptions**: a throwing listener does not stop the round.
  After the last round, `set` throws `ObserverNotificationError` with all
  failures in `errors`.

## Usage

```ts
import { Observable } from "./src/observable.ts";

const obs = new Observable(0);
const unsubscribe = obs.subscribe((value, previous) => {
  console.log(`${previous} -> ${value}`);
}, /* priority */ 0);

obs.set(1); // logs "0 -> 1"
unsubscribe();
```

## Tests

Requires Node >= 22.18 (native TypeScript type stripping).

```sh
npm test
```
