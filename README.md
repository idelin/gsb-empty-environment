# guarded-navigator

A small TypeScript navigation-state library with sequential async guards.

## Guarantees

- **Sequential guards** — guards run in registration order; each may allow
  (``true`/`undefined`), deny (`false` / `{ type: "deny" }`), or redirect
  (string / `{ type: "redirect", to }`). A redirect restarts the guard chain
  for the new target.
- **Stale-navigation invalidation** — every `navigate()`/`back()` call
  supersedes the previous round. Late-arriving guard promises from an older
  round resolve to `{ status: "superseded" }` and never touch the address or
  history.
- **Per-round loop detection** — redirect loops are detected against the set
  of targets visited in the current navigation round only; global history is
  never consulted, so revisiting a target in a later round is fine.
- **Single commit** — history is appended (or the back-index moved) exactly
  once, only after every guard allows the final target. If a `back()` round
  is redirected away from the previous entry, the target is pushed instead.

## API

```ts
const nav = new Navigator("/");
const remove = nav.addGuard(async (to, from) => {
  if (to === "/admin" && !loggedIn()) return "/login"; // redirect
  if (to === "/closed") return false;                  // deny
  // otherwise allow
});

await nav.navigate("/admin"); // { status: "committed", path: "/login" }
await nav.back();             // guarded move to the previous entry
nav.current;                  // committed address
nav.history;                  // committed entries
```

Outcomes: `committed` · `cancelled` (denied) · `superseded` (a newer
navigation won) · `loop` (redirect cycle, with the visited chain).

## Tests

Requires Node ≥ 22.6 (uses native TypeScript type-stripping):

```sh
npm test
```
