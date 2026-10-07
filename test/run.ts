/**
 * Test runner (no external deps): fixed cases plus exhaustive cross-checks
 * against the brute-force reference on short texts.
 */

import {
  NonOverlappingRepeat,
  longestNonOverlappingRepeat,
  maxNonOverlappingStarts,
  occurrenceStarts,
  selectNonOverlapping,
} from "../src/index";
import {
  bruteCanonicalStarts,
  bruteForceLongest,
  bruteMaxNonOverlappingCount,
  compareStartLists,
} from "./bruteForce";

let failures = 0;
let checks = 0;

function fail(message: string): void {
  failures++;
  console.error(`FAIL: ${message}`);
}

function expectEqual<T>(actual: T, expected: T, label: string): void {
  checks++;
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) fail(`${label}: expected ${e}, got ${a}`);
}

function expectThrows(fn: () => unknown, label: string): void {
  checks++;
  try {
    fn();
  } catch {
    return;
  }
  fail(`${label}: expected a throw`);
}

// ---------- fixed cases ----------

// Empty input returns empty.
expectEqual(longestNonOverlappingRepeat("", 1), null, "empty text");
expectEqual(longestNonOverlappingRepeat("", 5), null, "empty text, larger k");

// Nothing qualifies.
expectEqual(longestNonOverlappingRepeat("abc", 2), null, "no repeat");
expectEqual(longestNonOverlappingRepeat("ab", 3), null, "k too large");

// k = 1: the whole text is the longest substring occurring once.
expectEqual(
  longestNonOverlappingRepeat("hello", 1),
  { substring: "hello", starts: [0] },
  "k=1 whole text",
);

// Occurrence count vs selectable non-overlapping count:
// "aaa" occurs twice in "aaaa" (starts 0, 1) but the intervals overlap,
// so the longest substring with 2 non-overlapping occurrences is "aa".
expectEqual(
  longestNonOverlappingRepeat("aaaa", 2),
  { substring: "aa", starts: [0, 2] },
  "overlap excludes aaa",
);
expectEqual(
  longestNonOverlappingRepeat("aaaaa", 2),
  { substring: "aa", starts: [0, 2] },
  "aaaaa k=2",
);
expectEqual(
  longestNonOverlappingRepeat("aaaa", 3),
  { substring: "a", starts: [0, 1, 2] },
  "aaaa k=3",
);
expectEqual(
  longestNonOverlappingRepeat("ababab", 3),
  { substring: "ab", starts: [0, 2, 4] },
  "ababab k=3",
);
expectEqual(
  longestNonOverlappingRepeat("ababab", 2),
  { substring: "ab", starts: [0, 2] },
  "ababab k=2 (aba/abab only overlap)",
);

// Byte-order tie-break: "a" and "b" both occur twice without overlap.
expectEqual(
  longestNonOverlappingRepeat("bacab", 2),
  { substring: "a", starts: [1, 3] },
  "byte-order tie-break",
);

// UTF-8 byte order, not UTF-16 code-unit order. Both candidates are 2
// code units long and occur twice without overlap: the grinning face
// U+1F600 (UTF-16 D83D DE00, UTF-8 F0 9F 98 80) at 0 and 3, and the pair
// U+E000 U+E000 (UTF-16 EE00 EE00, UTF-8 EE 80 80 EE 80 80) at 6 and 9.
// Code-unit order ranks the face first (D83D < EE00), byte order ranks
// the U+E000 pair first (EE < F0).
expectEqual(
  longestNonOverlappingRepeat(
    "\u{1F600}a\u{1F600}b\uE000\uE000c\uE000\uE000",
    2,
  ),
  { substring: "\uE000\uE000", starts: [6, 9] },
  "utf-8 byte order beats utf-16 order",
);

// Invalid k.
expectThrows(() => longestNonOverlappingRepeat("abc", 0), "k=0");
expectThrows(() => longestNonOverlappingRepeat("abc", -2), "k negative");
expectThrows(() => longestNonOverlappingRepeat("abc", 1.5), "k non-integer");

// ---------- occurrence count vs selectable count helpers ----------

expectEqual(occurrenceStarts("aaaa", "aa"), [0, 1, 2], "raw occurrences");
expectEqual(maxNonOverlappingStarts("aaaa", "aa"), [0, 2], "selectable occurrences");
expectEqual(occurrenceStarts("aaa", "aa"), [0, 1], "raw occurrences aaa");
expectEqual(maxNonOverlappingStarts("aaa", "aa"), [0], "selectable in aaa");
expectEqual(occurrenceStarts("ababab", "ab"), [0, 2, 4], "raw occurrences ab");
expectEqual(maxNonOverlappingStarts("ababab", "ab"), [0, 2, 4], "selectable ab");
expectThrows(() => occurrenceStarts("abc", ""), "empty sub");

// ---------- deterministic PRNG ----------

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomText(rand: () => number, alphabet: string, maxLength: number): string {
  const length = Math.floor(rand() * (maxLength + 1));
  let out = "";
  for (let i = 0; i < length; i++) {
    out += alphabet[Math.floor(rand() * alphabet.length)];
  }
  return out;
}

// ---------- exhaustive cross-check: interval selection ----------

{
  const rand = mulberry32(0x5eed);
  const iterations = 400;
  for (let iter = 0; iter < iterations; iter++) {
    const alphabet = ["ab", "abc", "a\u00E9b"][iter % 3]!;
    const text = randomText(rand, alphabet, 9);
    for (let len = 1; len <= text.length; len++) {
      const seen = new Set<string>();
      for (let i = 0; i + len <= text.length; i++) {
        const sub = text.substring(i, i + len);
        if (seen.has(sub)) continue;
        seen.add(sub);
        const starts = occurrenceStarts(text, sub);
        const greedy = selectNonOverlapping(starts, len);
        const bruteCount = bruteMaxNonOverlappingCount(starts, len);
        if (greedy.length !== bruteCount) {
          fail(
            `interval selection mismatch for ${JSON.stringify(sub)} in ` +
              `${JSON.stringify(text)}: greedy ${greedy.length} vs brute ${bruteCount}`,
          );
        }
        checks++;
        for (let k = 1; k <= bruteCount + 1; k++) {
          const limited = selectNonOverlapping(starts, len, k);
          const canonical = bruteCanonicalStarts(starts, len, k);
          if (canonical === null) {
            if (limited.length >= k) {
              fail(`unexpected k-selection for ${JSON.stringify(sub)} k=${k}`);
            }
          } else if (compareStartLists(limited, canonical) !== 0) {
            fail(
              `canonical starts mismatch for ${JSON.stringify(sub)} in ` +
                `${JSON.stringify(text)} k=${k}: ` +
                `[${limited}] vs [${canonical}]`,
            );
          }
          checks++;
        }
      }
    }
  }
  console.log(`interval-selection cross-check done (${iterations} texts)`);
}

// ---------- exhaustive cross-check: full search vs brute force ----------

{
  const rand = mulberry32(0xc0ffee);
  const iterations = 1500;
  for (let iter = 0; iter < iterations; iter++) {
    const alphabet = ["ab", "abc", "a\u00E9b", "\u{1F600}\uE000ab"][iter % 4]!;
    const text = randomText(rand, alphabet, 11);
    const k = 1 + Math.floor(rand() * 5);
    const actual: NonOverlappingRepeat | null = longestNonOverlappingRepeat(text, k);
    const expected: NonOverlappingRepeat | null = bruteForceLongest(text, k);
    const a = JSON.stringify(actual);
    const e = JSON.stringify(expected);
    if (a !== e) {
      fail(`search mismatch text=${JSON.stringify(text)} k=${k}: expected ${e}, got ${a}`);
    }
    checks++;
  }
  console.log(`full-search cross-check done (${iterations} texts)`);
}

console.log(`${checks} checks, ${failures} failures`);
if (failures > 0) {
  throw new Error(`${failures} test failure(s)`);
}
