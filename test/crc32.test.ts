import { test } from "node:test";
import assert from "node:assert/strict";
import { crc32, digestToHex } from "../src/crc32.ts";

test("crc32 matches the IEEE check vector", () => {
  // Standard CRC-32 check value: crc32("123456789") = 0xCBF43926.
  assert.equal(crc32("123456789"), 0xcbf43926);
});

test("crc32 of empty input is zero", () => {
  assert.equal(crc32(""), 0);
  assert.equal(crc32(new Uint8Array(0)), 0);
});

test("crc32 accepts strings and bytes identically", () => {
  const text = "member-7|alive|42";
  assert.equal(crc32(text), crc32(Buffer.from(text, "utf8")));
});

test("crc32 is deterministic and input-sensitive", () => {
  assert.equal(crc32("node-a"), crc32("node-a"));
  assert.notEqual(crc32("node-a"), crc32("node-b"));
});

test("digestToHex renders 4 bytes as 8 hex chars", () => {
  assert.equal(digestToHex(0xcbf43926), "cbf43926");
  assert.equal(digestToHex(0), "00000000");
  assert.equal(digestToHex(0xffffffff), "ffffffff");
});
