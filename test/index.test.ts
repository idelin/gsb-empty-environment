import { test } from "node:test";
import assert from "node:assert/strict";
import { ChunkedText, MatchIndex, findAll } from "../src/index.ts";

function drain(cursor: { next(): IteratorResult<{ patternId: string; start: number }> }) {
  const out: Array<[string, number]> = [];
  for (;;) {
    const r = cursor.next();
    if (r.done) return out;
    out.push([r.value.patternId, r.value.start]);
  }
}

test("index tracks matches and updates differentially after edits", () => {
  const text = new ChunkedText("the quick brown fox jumps", 4);
  const index = new MatchIndex(text);
  index.addPattern("o", "o");
  index.addPattern("quick", "quick");
  assert.deepEqual(index.matches("o"), [12, 17]);
  assert.deepEqual(index.matches("quick"), [4]);

  text.replace(4, 9, "slow"); // "the slow brown fox jumps"
  assert.deepEqual(index.matches("o"), [6, 11, 16]); // 'o' in slow/brown/fox
  assert.deepEqual(index.matches("quick"), []);
  assert.deepEqual(findAll(text, "o"), index.matches("o"));
});

test("deleting a chunk boundary creates new matches that are found", () => {
  const text = new ChunkedText("aabaa", 3); // chunks: aab | aa
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  assert.deepEqual(index.matches("aa"), [0, 3]);
  text.replace(2, 3, ""); // delete 'b' at the boundary -> "aaaa"
  assert.equal(text.toString(), "aaaa");
  assert.deepEqual(index.matches("aa"), [0, 1, 2]); // match at 1 is new
});

test("insertion at a boundary creates overlapping matches", () => {
  const text = new ChunkedText("aaaa", 2); // aa | aa
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  assert.deepEqual(index.matches("aa"), [0, 1, 2]);
  text.replace(2, 2, "aa"); // aa aa aa -> "aaaaaa"
  assert.deepEqual(index.matches("aa"), [0, 1, 2, 3, 4]);
});

test("self-overlapping pattern across chunks", () => {
  const text = new ChunkedText("ababa", 2); // ab ab a
  const index = new MatchIndex(text);
  index.addPattern("aba", "aba");
  assert.deepEqual(index.matches("aba"), [0, 2]);
  text.replace(5, 5, "ba"); // -> "abababa"
  assert.deepEqual(index.matches("aba"), [0, 2, 4]);
});

test("empty pattern in the index: every position, updated after edits", () => {
  const text = new ChunkedText("abc", 2);
  const index = new MatchIndex(text);
  index.addPattern("empty", "");
  assert.deepEqual(index.matches("empty"), [0, 1, 2, 3]);
  text.replace(1, 2, "xy"); // "axyc"
  assert.deepEqual(index.matches("empty"), [0, 1, 2, 3, 4]);
  text.replace(0, 4, ""); // ""
  assert.deepEqual(index.matches("empty"), [0]);
});

test("multi-pattern pausable cursor yields merged overlapping results", () => {
  const text = new ChunkedText("abab", 2);
  const index = new MatchIndex(text);
  index.addPattern("ab", "ab");
  index.addPattern("ba", "ba");
  index.addPattern("abab", "abab");
  const cursor = index.query(["ab", "ba", "abab"]);
  const first = cursor.next();
  assert.deepEqual([first.value.patternId, first.value.start], ["ab", 0]);
  // pause here; resume later
  const rest = drain(cursor);
  assert.deepEqual(
    [[first.value.patternId, first.value.start], ...rest],
    [
      ["ab", 0],
      ["abab", 0],
      ["ba", 1],
      ["ab", 2],
    ],
  );
});

test("cursor created before an edit shifts past the edit (old-version cursor)", () => {
  const text = new ChunkedText("xxaayyaa", 3);
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  const cursor = index.query(["aa"]);
  assert.equal(cursor.next().value.start, 2);
  text.replace(0, 0, "aa"); // insert before the cursor -> "aaxxaayyaa"
  // The already-emitted match (old 2, now 4) is not re-emitted; the cursor
  // continues in the new coordinate space and reports only new matches.
  assert.deepEqual(drain(cursor), [["aa", 8]]);
});

test("cursor inside the edited range rewinds to the affected window", () => {
  const text = new ChunkedText("xxaayyaa", 3);
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  const cursor = index.query(["aa"]);
  assert.equal(cursor.next().value.start, 2); // cursor sits inside [1, 4)
  text.replace(1, 4, ""); // -> "xyyaa"
  assert.deepEqual(drain(cursor), [["aa", 3]]);
});

test("cursor from a given offset", () => {
  const text = new ChunkedText("aaaa", 2);
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  assert.deepEqual(drain(index.query(["aa"], 1)), [
    ["aa", 1],
    ["aa", 2],
  ]);
});

test("incremental update re-reads only the affected window", () => {
  const body = "z".repeat(997) + "aba" + "z".repeat(1000);
  const text = new ChunkedText(body, 8);
  const index = new MatchIndex(text);
  index.addPattern("aba", "aba");
  assert.deepEqual(index.matches("aba"), [997]);
  text.charsRead = 0;
  text.replace(1000, 1000, "ba"); // grow the match region: "ababa"
  assert.deepEqual(index.matches("aba"), [997, 999]);
  // Scan window: lo = 1000 - 2 = 998, hi = 1001 -> 6 code units, plus a
  // bounded amount for the edit itself (surrogate checks + one chunk).
  // Nowhere near the full length of 2002.
  assert.ok(text.charsRead <= 24, `charsRead=${text.charsRead}`);
});
