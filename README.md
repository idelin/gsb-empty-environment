# optimal-fit-linebreaker

Minimum-cost line breaking (word wrap) over **fixture-provided** node trees and
glyph metrics. No browser layout engine is involved: every width comes from
`src/metrics.ts` and every paragraph is a box/glue/penalty node tree built by
`src/nodes.ts`.

Requires Node.js >= 22.6 (TypeScript is executed directly via type stripping;
there is no build step and there are no dependencies).

```sh
npm run demo   # run the worked samples
npm test       # node:test suite
```

## Cost model (`src/cost.ts`)

Each candidate line is measured from the node tree (`measureLine`):

- natural width `W`, total glue `stretch` / `shrink`, hyphen flag;
- `remaining = lineWidth - W` (negative remaining width is reported);
- stretch/shrink badness `100 * ratio^3` (the final line stretches for free,
  like TeX's `\parfillskip`);
- overflow (`remaining < -shrink`) is always feasible — an unbreakable word may
  overflow — but pays `OVERFLOW_PENALTY + OVERFLOW_PER_UNIT * excess` and is
  reported as a warning (atomic overflow = "unbreakable word wider than the
  line", reported separately from shrinkable negative remaining width);
- breaking at a flagged penalty adds its hyphen penalty;
- adjacent lines pay `widthDiffWeight * |W_i - W_{i-1}|`.

## DP with history-constrained state (`src/breaker.ts`)

The DP state key carries the cross-line history, so the constraints are exact,
not heuristic:

- `hyphens` — hyphenated lines used so far, bounded by `maxHyphens`;
- `prevHyphenated` — forbids two consecutive hyphenated lines;
- `prevWidth` — feeds the adjacent-line width-difference penalty.

Ties are broken by the **earlier** break positions (lexicographically smallest
break sequence), matching the exhaustive checker. A relaxation `budget` bounds
the search; on exhaustion the result is reported as `budget-exhausted` with **no
optimality claim**, separately from normal output.

Every result carries:

- per-line metrics (width, remaining, badness, hyphen/widthDiff/overflow costs);
- decision evidence: all candidate breaks from the frontier state the optimal
  path passed through, including candidates rejected by the hyphen budget or
  the consecutive-hyphen ban;
- a step-by-step certificate whose cumulative costs sum to the optimal total.

## Incremental editing (`src/incremental.ts`)

`createSession` keeps the DP frontier. `applyEdit` replaces a node range and:

- reuses frontier states at break positions inside the unchanged prefix
  (their costs depend only on content before them);
- re-expands earlier positions only toward breaks **beyond** the edit (a line
  starting before the edit may cross into edited content);
- reports reused/recomputed state counts and the new optimal cost.

The result is identical to a full recompute (verified in tests).

## Verification

- `src/exhaustive.ts` brute-forces every legal break sequence for small
  paragraphs and confirms the DP optimum and the tie-break choice.
- `src/greedy.ts` is a first-fit heuristic, explicitly labelled **not optimal**;
  the `greedyTrap` fixture shows it paying 806 where the optimum pays 150.
- `test/breaker.test.ts` (node:test) covers metrics, DP vs exhaustive,
  tie-breaks, the greedy trap, unbreakable overflow, hyphen budget,
  consecutive-hyphen ban, width-diff penalty, incremental reuse, budget
  exhaustion, and certificate consistency.
