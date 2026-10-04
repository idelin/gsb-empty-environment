import assert from "node:assert/strict";
import { test } from "./harness.mjs";
import {
  assertWellFormedUtf8,
  countScalars,
  isWellFormedUtf8,
  scalarOffsetToUtf16Index,
  utf8ByteLength,
  InvalidEncodingError,
} from "../dist/utf8.js";

test("utf8: accepts well-formed text", () => {
  for (const s of ["", "abc", "é", "中", "😀", "🧡", "𐍈", "a🙂b", "́", "👩‍💻", "ﷺ"]) {
    assert.equal(isWellFormedUtf8(s), true, JSON.stringify(s));
  }
});

test("utf8: rejects lone surrogates (not UTF-8 encodable)", () => {
  const bad = [
    "\uD800",
    "\uDC00",
    "a\uD800",
    "\uD800a",
    "a\uDC00b",
    "\uD800\uD800",
    "\uDC00\uD800",
    "😀".slice(0, 1),
    "😀".slice(1),
    "text\uD83Dmore",
  ];
  for (const s of bad) {
    assert.equal(isWellFormedUtf8(s), false, JSON.stringify(s));
  }
});

test("utf8: scalar and byte counts match independent computation", () => {
  const samples = [
    "",
    "a",
    "é",
    "中",
    "😀",
    "🧡𐍈",
    "hello 世界😀",
    "👩‍💻",
    "é".repeat(100),
    "😀".repeat(33),
    "aé中😀b🧡𐍈 ",
  ];
  for (const s of samples) {
    assert.equal(countScalars(s), Array.from(s).length, JSON.stringify(s));
    assert.equal(utf8ByteLength(s), Buffer.byteLength(s, "utf8"), JSON.stringify(s));
  }
});

test("utf8: scalar offset -> utf16 index never splits surrogate pairs", () => {
  const s = "aé中😀b🧡𐍈";
  const scalars = Array.from(s);
  for (let k = 0; k <= scalars.length; k++) {
    const idx = scalarOffsetToUtf16Index(s, k);
    assert.equal(s.slice(0, idx), scalars.slice(0, k).join(""), "offset " + k);
    assert.equal(s.slice(idx), scalars.slice(k).join(""), "offset " + k);
  }
});

test("utf8: assertWellFormedUtf8 throws InvalidEncodingError", () => {
  assert.throws(() => assertWellFormedUtf8("\uD83D"), InvalidEncodingError);
  assert.doesNotThrow(() => assertWellFormedUtf8("😀"));
  assert.doesNotThrow(() => assertWellFormedUtf8(""));
});
