import { test } from "node:test";
import assert from "node:assert/strict";
import { ChunkedText, splitChunks, findAll, isHighSurrogate, isLowSurrogate } from "../src/index.ts";

test('"aaaa" / "aa" yields overlapping matches 0, 1, 2 at any chunk size', () => {
  for (const chunkSize of [1, 2, 3, 4, 10]) {
    const text = new ChunkedText("aaaa", chunkSize);
    assert.deepEqual(findAll(text, "aa"), [0, 1, 2], `chunkSize=${chunkSize}`);
  }
});

test("pattern spanning three chunks matches", () => {
  const text = new ChunkedText("xabcdefy", 2); // xa bc de fy
  assert.equal(text.chunkCount, 4);
  assert.deepEqual(findAll(text, "abcde"), [1]); // spans chunks 1..3
  assert.deepEqual(findAll(text, "xabcdefy"), [0]); // spans all four chunks
});

test("search from a given offset", () => {
  const text = new ChunkedText("aaaa", 2);
  assert.deepEqual(findAll(text, "aa", { from: 1 }), [1, 2]);
  assert.deepEqual(findAll(text, "aa", { from: 2 }), [2]);
  assert.deepEqual(findAll(text, "aa", { from: 3 }), []);
  assert.throws(() => findAll(text, "aa", { from: 5 }), RangeError);
});

test("case-exact by default, optional case-insensitive", () => {
  const text = new ChunkedText("aAaa", 2);
  assert.deepEqual(findAll(text, "aa"), [2]);
  assert.deepEqual(findAll(text, "aa", { caseSensitive: false }), [0, 1, 2]);
  assert.deepEqual(findAll(text, "AA"), []);
  assert.deepEqual(findAll(text, "AA", { caseSensitive: false }), [0, 1, 2]);
});

test("empty pattern matches at every position 0..length", () => {
  const text = new ChunkedText("abc", 2);
  assert.deepEqual(findAll(text, ""), [0, 1, 2, 3]);
  assert.deepEqual(findAll(text, "", { from: 2 }), [2, 3]);
  assert.deepEqual(findAll(new ChunkedText("", 4), ""), [0]);
});

test("chunk boundaries never split surrogate pairs", () => {
  const s = "a😀b😀c"; // 😀 = 2 UTF-16 code units
  for (const chunkSize of [1, 2, 3, 4]) {
    const text = new ChunkedText(s, chunkSize);
    assert.equal(text.toString(), s);
    for (let i = 0; i < text.chunkCount - 1; i++) {
      const tail = text.chunkAt(i).charCodeAt(text.chunkAt(i).length - 1);
      const head = text.chunkAt(i + 1).charCodeAt(0);
      assert.ok(
        !(isHighSurrogate(tail) && isLowSurrogate(head)),
        `chunkSize=${chunkSize} chunk ${i} splits a pair`,
      );
    }
    assert.deepEqual(findAll(text, "😀"), [1, 4], `chunkSize=${chunkSize}`);
  }
});

test("edits may not split surrogate pairs", () => {
  const text = new ChunkedText("a😀b", 2);
  assert.throws(() => text.replace(2, 3, "x"), /surrogate/); // start inside pair
  assert.throws(() => text.replace(1, 2, "x"), /surrogate/); // end inside pair
  text.replace(1, 3, "x"); // replacing the whole pair is fine
  assert.equal(text.toString(), "axb");
});

test("from-offset inside a surrogate pair is moved past the pair", () => {
  const text = new ChunkedText("a😀b", 2); // pair at [1,3)
  assert.deepEqual(findAll(text, "", { from: 2 }), [3, 4]);
  assert.deepEqual(findAll(text, "b", { from: 2 }), [3]);
});

test("LF and CRLF line breaks, including CRLF split across chunks", () => {
  const text = new ChunkedText("a\r\nb\nc\r\n", 2); // a\r \nb \nc \r\n
  assert.deepEqual(findAll(text, "\r\n"), [1, 6]);
  assert.deepEqual(findAll(text, "\n"), [2, 4, 7]);
  assert.deepEqual(findAll(text, "\r"), [1, 6]);
  assert.deepEqual(findAll(text, "\n", { caseSensitive: true }), [2, 4, 7]);
});

test("splitChunks keeps pairs whole even with chunkSize 1", () => {
  assert.deepEqual(splitChunks("😀x", 1), ["😀", "x"]);
  assert.deepEqual(splitChunks("", 3), [""]);
});

test("search reads only a bounded window, never the whole text", () => {
  const text = new ChunkedText("z".repeat(1000) + "needle" + "z".repeat(1000), 7);
  text.charsRead = 0;
  assert.deepEqual(findAll(text, "needle", { from: 990 }), [1000]);
  // Single streaming pass over [990, 2006) plus the clamp check: 1018 reads,
  // and never a full-text concatenation (which would cost 2006+).
  assert.ok(text.charsRead <= 1018, `charsRead=${text.charsRead}`);
});
