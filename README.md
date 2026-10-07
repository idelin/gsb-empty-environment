# tree-events

DOM-style event dispatch (capture / target / bubble) over a virtual tree of
`EventNode` objects, written in TypeScript with no runtime dependencies.

## Usage

```ts
import { EventNode, TreeEvent } from "tree-events";

const root = new EventNode("root");
const child = new EventNode("child");
root.appendChild(child);

root.addEventListener("tap", (e) => console.log("capture", e.eventPhase), { capture: true });
child.addEventListener("tap", () => console.log("target"), { once: true });

const result = child.dispatchEvent(new TreeEvent("tap"));
// result.canceled: preventDefault() was called on a cancelable event
// result.errors: errors thrown by listeners (dispatch continues)
```

## Semantics

- **Phases**: capture (root -> parent) -> target -> bubble (parent -> root).
  At the target, capture and bubble listeners both run in registration order.
  `new TreeEvent(type, { bubbles: false })` skips the bubble phase.
- **Path snapshot**: the ancestor path is computed when dispatch begins;
  adding/removing nodes during dispatch does not change the current path.
- **Listener lists**: snapshotted per node when that node starts invoking.
  Removals take effect immediately (a removed listener is skipped even
  mid-node); listeners added during dispatch run only if their node has not
  been invoked yet.
- **once**: the listener is removed *before* invocation, so a re-entrant
  dispatch from inside it will not re-trigger it.
- **stopPropagation()**: later nodes are skipped; the current node's
  remaining listeners still run. **stopImmediatePropagation()** also stops
  the current node's remaining listeners.
- **Errors**: listener exceptions are caught, collected into
  `DispatchResult.errors`, and dispatch continues. Errors from a re-entrant
  (nested) dispatch stay in that dispatch's own result.
- **Re-entrancy**: dispatching another event from inside a listener is
  supported; dispatching the same event object while it is in flight throws.

## Develop

```sh
npm install   # requires the typescript devDependency
npm test      # tsc build + run call-log assertion suite
```
