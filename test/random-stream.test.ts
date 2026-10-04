import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { RandomStream, encodeName } from "../src/random-stream.ts";

// ---------------------------------------------------------------------------
// Cross-platform fixed vectors. These constants are part of the spec: any
// conforming implementation on any platform must reproduce them exactly.
// ---------------------------------------------------------------------------

test("fixed vector: root stream from seed 0 (splitmix64 reference)", () => {
  const s = RandomStream.fromSeed(0n);
  const expected = [
    0xe220a8397b1dcdafn,
    0x6e789e6aa1b965f4n,
    0x06c45d188009454fn,
    0xf88bb8a8724c81ecn,
    0x1b39896a51a8749bn,
    0x53cb9f0c747ea2ean,
    0x2c829abe1f4532e1n,
    0xc584133ac916ab3cn,
  ];
  assert.deepEqual(
    Array.from({ length: expected.length }, () => s.nextUint64()),
    expected,
  );
});

test("fixed vector: string seed", () => {
  const s = RandomStream.fromSeed("tree-random-stream/v1");
  assert.deepEqual(
    Array.from({ length: 4 }, () => s.nextUint32()),
    [691060182, 640807071, 2363853022, 1762219825],
  );
});

test("fixed vector: hierarchical path seed 42 -> suite -> case-1", () => {
  const s = RandomStream.fromSeed(42n).child("suite").child("case-1");
  assert.deepEqual(s.take(4), [
    0.3825869501159833,
    0.6279205551462711,
    0.9662828310643692,
    0.8270106988376241,
  ]);
});

test("fixed vector: bounded integers", () => {
  const s = RandomStream.fromSeed(7n).child("dice");
  assert.deepEqual(
    Array.from({ length: 6 }, () => s.nextInt(1, 7)),
    [4, 4, 2, 6, 3, 6],
  );
});

// ---------------------------------------------------------------------------
// Tree semantics
// ---------------------------------------------------------------------------

test("adding sibling tests never changes existing streams", () => {
  const root = RandomStream.fromSeed(123n);

  const before = root.child("alpha").take(8);

  // Derive extra siblings, interleave draws, derive more — order irrelevant.
  root.child("beta").take(100);
  root.child("gamma").take(50);
  const after = root.child("alpha").take(8);

  assert.deepEqual(after, before);
});

test("sibling derivation order does not matter", () => {
  const root1 = RandomStream.fromSeed(1n);
  const a1 = root1.child("a");
  const b1 = root1.child("b");

  const root2 = RandomStream.fromSeed(1n);
  const b2 = root2.child("b");
  const a2 = root2.child("a");

  assert.deepEqual(a1.take(16), a2.take(16));
  assert.deepEqual(b1.take(16), b2.take(16));
});

test("drawing from a parent never affects its children", () => {
  const root = RandomStream.fromSeed(9n);
  const early = root.child("t").take(8);
  root.take(1000);
  const late = root.child("t").take(8);
  assert.deepEqual(late, early);
});

test("renaming a test changes its stream", () => {
  const root = RandomStream.fromSeed(5n);
  const original = root.child("login").take(8);
  const renamed = root.child("login-v2").take(8);
  assert.notDeepEqual(renamed, original);
});

test("nested paths are order-sensitive and unique", () => {
  const root = RandomStream.fromSeed(0n);
  const ab = root.child("a").child("b").take(8);
  const ba = root.child("b").child("a").take(8);
  const flat = root.child("a:b").take(8);
  assert.notDeepEqual(ab, ba);
  assert.notDeepEqual(ab, flat);
});

// ---------------------------------------------------------------------------
// Canonical encoding & ambiguity rejection
// ---------------------------------------------------------------------------

test("canonical encoding is length-prefixed UTF-8", () => {
  assert.deepEqual([...encodeName("ab")], [...new TextEncoder().encode("2:ab")]);
  // Multi-byte: 'é' is 2 UTF-8 bytes in NFC.
  assert.deepEqual(
    [...encodeName("é")],
    [...new TextEncoder().encode("2:é")],
  );
});

test("rejects names with ambiguous encodings", () => {
  const s = RandomStream.fromSeed(0n);
  assert.throws(() => s.child(""), TypeError);
  assert.throws(() => s.child("a/b"), RangeError);
  assert.throws(() => s.child("a\\b"), RangeError);
  assert.throws(() => s.child("a\u0000b"), RangeError);
  assert.throws(() => s.child("a\u007fb"), RangeError);
  // NFD spelling of 'é' (e + combining accent) is ambiguous with NFC 'é'.
  assert.throws(() => s.child("e\u0301"), RangeError);
});

// ---------------------------------------------------------------------------
// Batched vs one-shot equivalence
// ---------------------------------------------------------------------------

test("batched take(n) equals n successive nextFloat() calls", () => {
  const batched = RandomStream.fromSeed(77n).child("batch").take(256);
  const oneShot = RandomStream.fromSeed(77n).child("batch");
  const expected = Array.from({ length: 256 }, () => oneShot.nextFloat());
  assert.deepEqual(batched, expected);
});

test("mixed batched and one-shot draws stay aligned", () => {
  const a = RandomStream.fromSeed(3n);
  const b = RandomStream.fromSeed(3n);
  const seqA = [...a.take(10), ...a.take(5), a.nextFloat(), ...a.take(100)];
  const seqB = b.take(116);
  assert.deepEqual(seqA, seqB);
  assert.equal(a.position, 116n);
});

// ---------------------------------------------------------------------------
// Determinism / no host randomness
// ---------------------------------------------------------------------------

test("same seed always reproduces the same stream", () => {
  for (let i = 0; i < 3; i++) {
    assert.deepEqual(
      RandomStream.fromSeed("repeatable").take(4),
      RandomStream.fromSeed("repeatable").take(4),
    );
  }
});

test("implementation never uses host random sources", () => {
  const source = readFileSync(
    new URL("../src/random-stream.ts", import.meta.url),
    "utf8",
  );
  for (const forbidden of ["Math.random", "crypto", "Date.now", "Math="]) {
    assert.ok(
      !source.includes(forbidden),
      `source must not reference ${forbidden}`,
    );
  }
});

test("rejects invalid seeds and ranges", () => {
  assert.throws(() => RandomStream.fromSeed(1.5), TypeError);
  assert.throws(() => RandomStream.fromSeed(Number.MAX_SAFE_INTEGER + 1), TypeError);
  const s = RandomStream.fromSeed(0n);
  assert.throws(() => s.nextInt(5, 5), RangeError);
  assert.throws(() => s.nextInt(0, -1), RangeError);
  assert.throws(() => s.take(-1), RangeError);
});
