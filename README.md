# Directional Focus Navigation (TypeScript, zero-dependency)

Arrow-key focus navigation over **nested scroll containers** and **modal
regions**, driven entirely by fixture data — rectangles, visibility/occlusion,
logical order and async mount states. No browser layout engine is involved in
any core computation; glyph metrics come from a fixture table.

Requires Node.js >= 23.6 (runs TypeScript directly via type stripping).

```sh
npm test        # node --test (Node.js built-in test runner)
npm run sample  # replayable end-to-end sample with transcript
```

## Architecture

| File | Role |
| --- | --- |
| `src/geometry.ts` | Rect math, main/cross-axis deltas, published edge-based candidacy rule, scroll-to-reveal deltas |
| `src/tree.ts` | `LayoutTree`: nodes (rect, focusable, modal, scrollable, collapsed, mount state, occlusion), `layoutVersion` bumped on every mutation, deferred mount/scroll operations |
| `src/navigator.ts` | Candidate planning: directional scoring, wrap-around, legal fallback, modal scoping |
| `src/controller.ts` | `FocusController`: async orchestration — plan, request mount/scroll, validate layout version, commit or re-plan; staleness; focus recovery |
| `src/announcer.ts` | Screen-reader sync point; announcing an invalid node is a hard error |
| `src/replay.ts` | Deterministic keyboard-event replay harness |
| `fixtures/` | Glyph-metric table and scene builders (all layout inputs) |
| `samples/replay-sample.ts` | Replayable sample journey with transcript |

## Published navigation rules

1. **Candidacy (edge-based, Android FocusFinder style):** `dst` is a
   directional candidate from `src` iff it extends past `src` on the far edge
   and starts at-or-beyond `src`'s near edge on the main axis
   (`isDirectionalCandidate` in `src/geometry.ts`).
2. **Main-axis priority scoring:** `score = mainDist + 2 * crossDist` over
   element centers; the main axis dominates and diagonal candidates are
   penalized by their cross-axis deviation (`CROSS_AXIS_PENALTY`). Ties break
   by logical (reading) order.
3. **Wrap-around:** with no directional candidate, focus jumps to the
   extreme opposite edge (tie-break: cross distance, then logical order).
4. **Legal fallback:** with wrap disabled, focus falls back to the next
   eligible node in logical order, cyclically within the scope.
5. **Modal containment:** candidates are restricted to the nearest enclosing
   modal subtree of the current focus — once focus enters a modal it cannot
   escape; wrap and fallback are scoped the same way.
6. **Eligibility:** occluded nodes (covered by a mounted, visible occluder at
   their center) and collapsed subtrees are never candidates. Unmounted and
   off-screen nodes *are* candidates — they become reachable via mount/scroll.

## Async protocol

`FocusController.handleKey` never focuses a stale or invisible node:

1. Recover focus first if the current node was deleted/collapsed
   (deterministic fallback to the first focusable-visible node in logical
   order).
2. Plan a candidate from the current layout; record `layoutVersion`.
3. Request mount (unmounted ancestors) and/or scroll (off-screen target).
4. On completion: if a newer key press arrived, the result is **stale** and
   discarded. If `layoutVersion` changed, the target is **re-validated**
   against the new layout (scrolls change geometry; nodes may have been
   deleted or moved).
5. A deleted/invisible candidate is excluded and the controller **re-plans**
   — bounded by the node count, so there is no unbounded waiting loop.
6. Every committed focus change is announced to the `FocusAnnouncer`
   (screen-reader sync); announcing an invalid node throws.

## Test results

`npm test` — 22/22 passing (Node v23.9.0, `node:test`):

- `test/navigator.test.ts` — main-axis priority for diagonal candidates,
  wrap-around, legal fallback, modal containment, occlusion/collapse/lazy
  eligibility.
- `test/controller.test.ts` — scroll-into-view, lazy mount, slow-result
  expiry on rapid key presses, mid-flight deletion re-plan, geometry change
  during scroll, focus recovery on delete/collapse, no-op termination,
  nested scroller offsets.
- `test/replay.test.ts` — scripted keyboard-event replays asserting no focus
  loss, announcer/focus sync, modal trapping, stale-result expiry and
  convergence of nested-scroll journeys.
- `test/metrics.test.ts` — glyph metrics and fixture-derived node sizes.

```
ℹ tests 22
ℹ pass 22
ℹ fail 0
```

## Sample

`npm run sample` replays a scripted journey (rapid key presses expiring a slow
scroll, lazy mount, mid-flight deletion, focus recovery, modal trapping) and
prints the full transcript plus the screen-reader announcement log, ending
with `focus valid: true`, `announcer in sync: true`,
`pending async work: false`.
