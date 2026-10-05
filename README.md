# batch-spell-replace

Batch spelling replacement over a rope, in TypeScript. All rules are matched
against the **original** text simultaneously, so a replacement can never
trigger another rule.

## Semantics

- **Word boundaries** — patterns match case-insensitively, only where both
  ends sit on a non-word character (`[A-Za-z0-9_]` delimits words).
- **Finite case table** — replacements are stored in canonical lowercase and
  rendered per match via `CASE_TRANSFORMS` (`lower` / `upper` /
  `capitalized`); anything else (e.g. mixed case) falls back to `lower`.
- **Conflicts** — overlapping candidates are resolved by priority
  (descending), then rule id (ascending). Winners are reported in `adopted`,
  losers in `shadowed` (with `shadowedBy`), both carrying original ranges.
- **Zero-length disabled** — rules with an empty pattern (or
  `enabled: false`) never participate.
- **Rope** — input is a chunked `Rope`; matches spanning chunk boundaries
  work without flattening.
- **Reversible** — `applyEdits(result.text, result.reverseEdits)` restores
  the original text exactly.

## Usage

```ts
import { Rope, replaceBatch, applyEdits } from "./src/index.ts";

const rope = Rope.from(["I love jav", "ascript and JS"]);
const result = replaceBatch(rope, [
  { id: "shrink", pattern: "javascript", replacement: "js", priority: 1 },
  { id: "grow", pattern: "js", replacement: "typescript", priority: 2 },
]);

result.text;      // patched text
result.adopted;   // winning candidates with original [start, end)
result.shadowed;  // losing candidates with original [start, end)
applyEdits(result.text, result.reverseEdits); // === rope.toString()
```

## Test

Requires Node >= 22.6 (type stripping; works out of the box on >= 23.6):

```sh
npm test
```
