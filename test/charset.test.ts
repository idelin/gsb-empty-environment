import { test } from "node:test";
import assert from "node:assert/strict";
import { CharsetError, resolveCharset, validateCharset, CHARSETS } from "../src/charset.ts";

test("named presets resolve and validate", () => {
  assert.ok(resolveCharset("lower", 16).startsWith("0123456789abcdef"));
  assert.ok(resolveCharset("upper", 16).startsWith("0123456789ABCDEF"));
  assert.equal(resolveCharset("base62", 62).length >= 62, true);
  assert.equal(resolveCharset("base62alt", 62)[10], "a");
});

test("explicit charset is used verbatim and case-sensitively", () => {
  const cs = resolveCharset("!@#$%^&*~=", 8);
  assert.equal(cs, "!@#$%^&*~=");
});

test("duplicate characters rejected (case-sensitive uniqueness)", () => {
  assert.throws(() => validateCharset("010", 2), CharsetError);
  // 'a' and 'A' are distinct — this is legal
  assert.doesNotThrow(() => validateCharset("aA", 2));
});

test("reserved syntax characters rejected", () => {
  for (const bad of ["-1", "+1", ".1", "(1", ")1"]) {
    assert.throws(() => validateCharset(bad, 2), CharsetError);
  }
});

test("base range enforced", () => {
  assert.throws(() => validateCharset("ab", 1), CharsetError);
  assert.throws(() => validateCharset(CHARSETS.base62, 63), CharsetError);
  assert.throws(() => validateCharset("ab", 2.5), CharsetError);
});

test("charset too short for base rejected", () => {
  assert.throws(() => validateCharset("abc", 4), CharsetError);
});
