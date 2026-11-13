import { test } from "node:test";
import assert from "node:assert/strict";
import { timingSafeEqualBytes, timingSafeEqualString } from "../src/timing.ts";

test("timingSafeEqualBytes: identical content compares equal", () => {
  assert.equal(timingSafeEqualBytes(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 3])), true);
});

test("timingSafeEqualBytes: different byte compares unequal", () => {
  assert.equal(timingSafeEqualBytes(new Uint8Array([1, 2, 3]), new Uint8Array([1, 9, 3])), false);
});

test("timingSafeEqualBytes: different length compares unequal", () => {
  assert.equal(timingSafeEqualBytes(new Uint8Array([1, 2]), new Uint8Array([1, 2, 3])), false);
});

test("timingSafeEqualBytes: empty arrays compare equal", () => {
  assert.equal(timingSafeEqualBytes(new Uint8Array([]), new Uint8Array([])), true);
});

test("timingSafeEqualBytes: 16-byte access-key sized inputs", () => {
  const key = globalThis.crypto.getRandomValues(new Uint8Array(16));
  assert.equal(timingSafeEqualBytes(key, new Uint8Array(key)), true);
  const tampered = new Uint8Array(key);
  tampered[0] = (tampered[0] ^ 1) as number;
  assert.equal(timingSafeEqualBytes(key, tampered), false);
});

test("timingSafeEqualString: equal and unequal strings", () => {
  assert.equal(timingSafeEqualString("alice:1", "alice:1"), true);
  assert.equal(timingSafeEqualString("alice:1", "alice:2"), false);
  assert.equal(timingSafeEqualString("", ""), true);
});
