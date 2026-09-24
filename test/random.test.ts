import { test } from "node:test";
import assert from "node:assert/strict";
import { ChunkedText, MatchIndex, isHighSurrogate, isLowSurrogate } from "../src/index.ts";

/** Deterministic PRNG (mulberry32) so failures are reproducible. */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Reference matcher: naive scan over the plain string. */
function naive(haystack: string, pattern: string, caseSensitive: boolean): number[] {
  const h = caseSensitive ? haystack : haystack.toLowerCase();
  const n = caseSensitive ? pattern : pattern.toLowerCase();
  const out: number[] = [];
  if (n.length === 0) {
    for (let i = 0; i <= haystack.length; i++) out.push(i);
    return out;
  }
  for (let i = 0; i + n.length <= haystack.length; i++) {
    if (h.startsWith(n, i)) out.push(i);
  }
  return out;
}

function splitsPair(s: string, off: number): boolean {
  return (
    off > 0 &&
    off < s.length &&
    isHighSurrogate(s.charCodeAt(off - 1)) &&
    isLowSurrogate(s.charCodeAt(off))
  );
}

function assertNoSplitChunks(text: ChunkedText): void {
  for (let i = 0; i < text.chunkCount - 1; i++) {
    const tail = text.chunkAt(i).charCodeAt(text.chunkAt(i).length - 1);
    const head = text.chunkAt(i + 1).charCodeAt(0);
    assert.ok(!(isHighSurrogate(tail) && isLowSurrogate(head)), `chunk ${i} splits a pair`);
  }
}

test("random edits cross-checked against naive search (with surrogate pairs)", () => {
  const rand = rng(0xC0FFEE);
  const alphabet = ["a", "a", "b", "a", "b", "\r", "\n", "😀", "á"];
  const patternPool = [
    { p: "aa", cs: true },   // self-overlapping
    { p: "ab", cs: true },
    { p: "aba", cs: true },
    { p: "a", cs: true },
    { p: "😀", cs: true },   // surrogate pair
    { p: "\r\n", cs: true }, // CRLF
    { p: "AB", cs: false },  // case-insensitive
    { p: "", cs: true },     // empty pattern
  ];
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];

  let totalReread = 0;
  let edits = 0;
  let worstWindow = 0;

  for (let iter = 0; iter < 60; iter++) {
    const len = 1 + Math.floor(rand() * 60);
    let s = "";
    while (s.length < len) s += pick(alphabet);
    const chunkSize = 1 + Math.floor(rand() * 5);
    const text = new ChunkedText(s, chunkSize);
    const index = new MatchIndex(text);
    const patterns = patternPool.map(({ p, cs }, i) => {
      index.addPattern(`p${i}`, p, cs);
      return { id: `p${i}`, p, cs };
    });
    for (const { id, p, cs } of patterns) {
      assert.deepEqual(index.matches(id), naive(s, p, cs), "initial build");
    }

    for (let step = 0; step < 8; step++) {
      // Random edit at a boundary that does not split a surrogate pair.
      let start = 0;
      let end = 0;
      for (;;) {
        start = Math.floor(rand() * (s.length + 1));
        end = start + Math.floor(rand() * (s.length - start + 1));
        if (!splitsPair(s, start) && !splitsPair(s, end)) break;
      }
      let insert = "";
      const insLen = Math.floor(rand() * 6);
      for (let k = 0; k < insLen; k++) insert += pick(alphabet);

      text.charsRead = 0;
      text.replace(start, end, insert);
      const reread = text.charsRead;
      s = s.slice(0, start) + insert + s.slice(end);
      assert.equal(text.toString(), s);
      assertNoSplitChunks(text);

      // Bound: every non-empty pattern re-scans at most insert + 2*m_i
      // code units; the edit itself reads <= 2*chunkSize + 4. Independent
      // of the text length.
      const nonEmpty = patterns.filter(({ p }) => p.length > 0);
      const sumM = nonEmpty.reduce((acc, { p }) => acc + p.length, 0);
      const window =
        nonEmpty.length * insert.length + 2 * sumM + 2 * chunkSize + 8;
      worstWindow = Math.max(worstWindow, window);
      totalReread += reread;
      edits++;
      assert.ok(
        reread <= window,
        `re-read ${reread} chars exceeds edit window ${window} (edit [${start},${end}) +${insert.length})`,
      );

      for (const { id, p, cs } of patterns) {
        assert.deepEqual(
          index.matches(id),
          naive(s, p, cs),
          `iter=${iter} step=${step} pattern=${JSON.stringify(p)} edit=[${start},${end})+${JSON.stringify(insert)} text=${JSON.stringify(s)}`,
        );
      }
    }
  }

  console.log(
    `random cross-check: ${edits} edits, avg re-read ${(totalReread / edits).toFixed(1)} chars/edit, worst allowed window ${worstWindow}`,
  );
});

test("deletion across many chunks only re-reads the affected window", () => {
  const text = new ChunkedText("a".repeat(5000), 7);
  const index = new MatchIndex(text);
  index.addPattern("aa", "aa");
  assert.equal(index.matches("aa").length, 4999);
  text.charsRead = 0;
  text.replace(2500, 2503, ""); // delete 3 chars in the middle
  assert.equal(index.matches("aa").length, 4996);
  // Window: lo = 2500 - 1, hi = 2499 -> empty scan; only edit-local reads.
  assert.ok(text.charsRead <= 32, `charsRead=${text.charsRead}`);
});
