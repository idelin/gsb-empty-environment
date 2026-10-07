# non-overlapping-repeat

TypeScript text-analysis library: find the longest substring of a text that
has at least `k` pairwise non-overlapping occurrences.

## Semantics

- An **occurrence** of a substring is any start index; overlaps count.
  `"aa"` occurs 3 times in `"aaaa"` (starts 0, 1, 2).
- A set of occurrences is **non-overlapping** when the half-open intervals
  `[start, start + length)` are pairwise disjoint. In `"aaaa"` at most 2
  occurrences of `"aa"` can be selected (starts 0 and 2). The raw occurrence
  count and the selectable non-overlapping count are different notions, and
  only the latter qualifies a substring.
- **Ranking**: longer substring wins; ties are broken by UTF-8 byte order of
  the substring, then by the ascending list of start positions.
- The reported `starts` are the lexicographically smallest list of `k`
  pairwise non-overlapping occurrence positions (i.e. the earliest ones).
- Empty input (or nothing qualifying) returns `null`.

## API

```ts
import {
  longestNonOverlappingRepeat,
  occurrenceStarts,
  maxNonOverlappingStarts,
  selectNonOverlapping,
  compareUtf8Bytes,
} from "non-overlapping-repeat";

longestNonOverlappingRepeat("aaaa", 2);
// { substring: "aa", starts: [0, 2] }
// "aaa" occurs twice (starts 0, 1) but only overlapping, so it loses.

longestNonOverlappingRepeat("", 1);        // null
longestNonOverlappingRepeat("abc", 2);     // null

occurrenceStarts("aaaa", "aa");            // [0, 1, 2]  (raw count 3)
maxNonOverlappingStarts("aaaa", "aa");     // [0, 2]     (selectable count 2)
```

`longestNonOverlappingRepeat(text, k)` throws a `RangeError` unless `k` is a
positive integer. Positions and lengths are UTF-16 code-unit indices, as
usual for JavaScript strings; only the tie-break comparison uses UTF-8 bytes.

## Implementation

`src/index.ts` binary-searches the answer length (qualification is monotone:
a qualifying substring implies qualifying prefixes), then scans all
substrings of that length, keeping those whose greedy non-overlapping
selection reaches `k`, and picks the UTF-8-byte-order minimum. Greedy
earliest-start selection is optimal for equal-length intervals and yields
the canonical (lexicographically smallest) start list.

## Build & test

```sh
npm install   # installs the typescript devDependency
npm test      # compiles to dist/ and runs node dist/test/run.js
```

`test/run.ts` contains fixed cases (including a UTF-8-vs-UTF-16 ordering
case) plus two exhaustive cross-checks on random short texts against
`test/bruteForce.ts`:

- interval selection: greedy `selectNonOverlapping` vs enumerating every
  subset of occurrence starts (max count and canonical k-subset);
- full search: `longestNonOverlappingRepeat` vs enumerating every substring
  of every length with brute-force interval selection.
