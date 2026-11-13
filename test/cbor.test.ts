import assert from "node:assert/strict";
import { test } from "node:test";
import { fromHex } from "../src/private-token/bytes.ts";
import {
  decodeCborCanonical,
  encodeCborCanonical,
} from "../src/private-token/cbor.ts";
import { ChallengeParseError } from "../src/private-token/errors.ts";

test("encodes the CBOR deterministic integer head cases", () => {
  assert.deepEqual(encodeCborCanonical(0), fromHex("00"));
  assert.deepEqual(encodeCborCanonical(23), fromHex("17"));
  assert.deepEqual(encodeCborCanonical(24), fromHex("1818"));
  assert.deepEqual(encodeCborCanonical(255), fromHex("18ff"));
  assert.deepEqual(encodeCborCanonical(256), fromHex("190100"));
  assert.deepEqual(encodeCborCanonical(65535), fromHex("19ffff"));
  assert.deepEqual(encodeCborCanonical(65536), fromHex("1a00010000"));
});

test("encodes and decodes byte strings, text and arrays", () => {
  const value = [1, "issuer.example", fromHex("001122"), ["a", "b"]];
  const encoded = encodeCborCanonical(value);
  assert.deepEqual(decodeCborCanonical(encoded), value);
  assert.equal(encoded[0], 0b100_00100);
});

test("rejects non-minimal integer encodings", () => {
  assert.throws(() => decodeCborCanonical(fromHex("1800")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("1900ff")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("1a000000ff")), ChallengeParseError);
});

test("rejects indefinite length and unsupported major types", () => {
  assert.throws(() => decodeCborCanonical(fromHex("9fff")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("5f42ff")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("20")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("f6")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("a0")), ChallengeParseError);
});

test("rejects trailing bytes and truncated input", () => {
  assert.throws(() => decodeCborCanonical(fromHex("0000")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("1901")), ChallengeParseError);
  assert.throws(() => decodeCborCanonical(fromHex("4200")), ChallengeParseError);
});

test("rejects invalid UTF-8 text", () => {
  assert.throws(() => decodeCborCanonical(fromHex("62ff")), ChallengeParseError);
});
