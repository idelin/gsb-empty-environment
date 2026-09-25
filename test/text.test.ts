import { test } from "node:test";
import assert from "node:assert/strict";
import { splitLines, isSurrogateBoundary, assertSafeOffset } from "../src/text.ts";

test("LF splitting with UTF-16 offsets", () => {
  const lines = splitLines("foo\nbar\n");
  assert.equal(lines.length, 2);
  assert.deepEqual(lines[0], { text: "foo", eol: "\n", start: 0, contentEnd: 3, end: 4 });
  assert.deepEqual(lines[1], { text: "bar", eol: "\n", start: 4, contentEnd: 7, end: 8 });
});

test("CRLF splitting with UTF-16 offsets", () => {
  const lines = splitLines("a\r\nb\r\n");
  assert.equal(lines.length, 2);
  assert.deepEqual(lines[0], { text: "a", eol: "\r\n", start: 0, contentEnd: 1, end: 3 });
  assert.deepEqual(lines[1], { text: "b", eol: "\r\n", start: 3, contentEnd: 4, end: 6 });
});

test("mixed LF and CRLF in one document", () => {
  const lines = splitLines("a\nb\r\nc");
  assert.equal(lines.length, 3);
  assert.equal(lines[0].eol, "\n");
  assert.equal(lines[1].eol, "\r\n");
  assert.equal(lines[2].eol, "");
  assert.equal(lines[2].start, 5);
  assert.equal(lines[2].text, "c");
});

test("no trailing newline and empty text", () => {
  assert.equal(splitLines("").length, 0);
  const lines = splitLines("solo");
  assert.equal(lines.length, 1);
  assert.equal(lines[0].eol, "");
});

test("repeated blank lines split into distinct lines", () => {
  const lines = splitLines("\n\n\n\n");
  assert.equal(lines.length, 4);
  for (const line of lines) assert.equal(line.text, "");
});

test("UTF-16 offsets count surrogate pairs as two units", () => {
  const lines = splitLines("😀\nfoo");
  assert.equal(lines[0].contentEnd, 2); // 😀 is two UTF-16 code units
  assert.equal(lines[1].start, 3);
});

test("surrogate boundary detection forbids splitting a pair", () => {
  const text = "x😀y"; // offsets: x=0, high=1, low=2, y=3
  assert.equal(isSurrogateBoundary(text, 0), true);
  assert.equal(isSurrogateBoundary(text, 1), true);
  assert.equal(isSurrogateBoundary(text, 2), false); // inside 😀
  assert.equal(isSurrogateBoundary(text, 3), true);
  assert.equal(isSurrogateBoundary(text, 4), true);
  assert.equal(isSurrogateBoundary(text, 5), false); // out of range
});

test("assertSafeOffset throws inside a surrogate pair", () => {
  const text = "x😀y";
  assert.throws(() => assertSafeOffset(text, 2), /surrogate pair/);
  assert.throws(() => assertSafeOffset(text, -1), /out of range/);
  assert.doesNotThrow(() => assertSafeOffset(text, 3));
});
