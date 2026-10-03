import { test } from "node:test";
import assert from "node:assert/strict";
import {
  encodeKey,
  fnv1_32,
  fnv1a_32,
  md5Hex,
  md5_32,
  murmur3_32,
} from "../src/hashes.ts";

const enc = (s: string) => encodeKey(s);

test("md5 matches RFC 1321 test vectors", () => {
  assert.equal(md5Hex(""), "d41d8cd98f00b204e9800998ecf8427e");
  assert.equal(md5Hex("abc"), "900150983cd24fb0d6963f7d28e17f72");
  assert.equal(
    md5Hex("The quick brown fox jumps over the lazy dog"),
    "9e107d9d372bb6826bd81d3542a419d6",
  );
});

test("md5_32 returns low 32 bits of the digest", () => {
  // md5("abc") = 90015098 3cd24fb0 ... -> first 4 bytes LE = 0x98500190
  assert.equal(md5_32(enc("abc")), 0x98500190);
});

test("murmur3 x86_32 matches reference vectors", () => {
  assert.equal(murmur3_32(enc("")), 0);
  assert.equal(murmur3_32(enc("hello")), 0x248bfa47);
  assert.equal(murmur3_32(enc("hello"), 42), 0xe2dbd2e1);
});

test("murmur3 handles every tail length", () => {
  // Distinct lengths must not collide with each other's empty-tail path.
  const values = new Set(
    ["a", "ab", "abc", "abcd", "abcde"].map((s) => murmur3_32(enc(s))),
  );
  assert.equal(values.size, 5);
});

test("fnv1a_32 matches canonical FNV test vectors", () => {
  assert.equal(fnv1a_32(enc("")), 0x811c9dc5);
  assert.equal(fnv1a_32(enc("a")), 0xe40c292c);
  assert.equal(fnv1a_32(enc("foobar")), 0xbf9cf968);
});

test("fnv1_32 matches canonical FNV test vectors", () => {
  assert.equal(fnv1_32(enc("")), 0x811c9dc5);
  assert.equal(fnv1_32(enc("a")), 0x050c5d7e);
  assert.equal(fnv1_32(enc("foobar")), 0x31f0b262);
});

test("hashes return unsigned 32-bit integers", () => {
  for (const h of [md5_32, murmur3_32, fnv1a_32]) {
    const v = h(enc("some key"));
    assert.ok(Number.isInteger(v) && v >= 0 && v <= 0xffffffff);
  }
});
