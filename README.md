# guarded-navigation

A small TypeScript navigation-state library: sequential async guards that can
allow, reject or redirect, with a history stack (back/forward/go).

## Guarantees

- **Sequential guards** — guards run one at a time in registration order and
  short-circuit on the first reject/redirect.
- **Guard decisions** — return `undefined`/`true` to allow, `false` to reject,
  `"/path"` or `{ redirect: "/path" }` to redirect.
- **Latest navigation wins** — every round gets a monotonic token; after each
  awaited guard the token is re-checked, so a late promise from a superseded
  navigation can never move the address or append history.
- **Redirect loops** — detected against the set of targets visited in the
  current round only; the same target is fine again in a later navigation.
- **Single commit** — history is mutated at exactly one point, after all
  guards allow. Redirecting a `back()`/`forward()` turns it into a push.

## Usage

```ts
import { Router } from "./src/navigation.ts";

const router = new Router("/");
router.beforeEach(async (to, from) => {
  if (to.path === "/admin" && !loggedIn()) return "/login"; // redirect
  if (to.path === "/readonly") return false;                // reject
  // otherwise allow
});

await router.navigate("/admin"); // -> committed at /login
await router.back();             // traversals run guards too
```

`navigate`/`back`/`forward`/`go` resolve to a `NavigationResult`:
`{ status: "committed", route }` or
`{ status: "cancelled", reason: "rejected" | "superseded" | "redirect-loop" | "out-of-range" }`.

## Tests

Requires Node >= 23.6 (native TypeScript type stripping).

```sh
npm test
```

The suite drives guards with manually-resolved promises to verify ordering,
superseded late promises, cancellation, back/forward and nested redirects.
