"use strict";
const assert = require("assert");
const { test } = require("./harness");
const {
  BUCKET_COUNT,
  utf8Bytes,
  fnv1a32,
  hashString,
  bucketKey,
  bucketOf,
} = require("../dist");

// Published FNV-1a 32-bit test vectors (Landon Noll's canonical set,
// also listed on Wikipedia's FNV article).
const FNV1A_32_VECTORS = [
  ["", 0x811c9dc5],
  ["a", 0xe40c292c],
  ["b", 0xe70c2de5],
  ["c", 0xe60c2c52],
  ["foobar", 0xbf9cf968],
];

test("fnv1a32 matches published public test vectors", () => {
  for (const [input, expected] of FNV1A_32_VECTORS) {
    assert.strictEqual(
      hashString(input),
      expected,
      `fnv1a32(${JSON.stringify(input)})`
    );
  }
});

test("hashing uses UTF-8 bytes, not UTF-16 code units", () => {
  // U+00E9 é -> 0xC3 0xA9 in UTF-8
  assert.deepStrictEqual([...utf8Bytes("é")], [0xc3, 0xa9]);
  // CJK: 3 bytes per character
  assert.deepStrictEqual(
    [...utf8Bytes("中文")],
    [0xe4, 0xb8, 0xad, 0xe6, 0x96, 0x87]
  );
  // Astral plane (surrogate pair in UTF-16) -> 4 UTF-8 bytes
  assert.strictEqual("🚀".length, 2);
  assert.deepStrictEqual([...utf8Bytes("🚀")], [0xf0, 0x9f, 0x9a, 0x80]);
});

test("unicode hash vectors (computed over UTF-8 bytes)", () => {
  assert.strictEqual(hashString("é"), 0x1e9de8c1);
  assert.strictEqual(hashString("中文"), 0xc488f045);
  assert.strictEqual(hashString("🚀"), 0x7fa78ada);
});

test("fnv1a32 accepts raw byte arrays", () => {
  assert.strictEqual(fnv1a32(new Uint8Array([])), 0x811c9dc5);
  assert.strictEqual(fnv1a32(new Uint8Array([0x61])), 0xe40c292c);
});

test("bucketOf stays inside [0, 9999] and is deterministic", () => {
  for (let i = 0; i < 2000; i++) {
    const b = bucketOf(`user-${i}`);
    assert.ok(Number.isInteger(b) && b >= 0 && b < BUCKET_COUNT, `bucket ${b}`);
    assert.strictEqual(bucketOf(`user-${i}`), b);
  }
  assert.strictEqual(BUCKET_COUNT, 10000);
});

test("bucketOf is hash mod 10000 of the exact key string", () => {
  const key = bucketKey(["salt", "flag", "user"]);
  assert.strictEqual(bucketOf(key), hashString(key) % 10000);
});

test("bucketKey parts cannot blur together", () => {
  assert.notStrictEqual(
    bucketKey(["ab", "c"]),
    bucketKey(["a", "bc"])
  );
});
