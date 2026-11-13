import assert from "node:assert/strict";
import { test } from "node:test";
import { fromHex } from "../src/private-token/bytes.ts";
import {
  decodeBase64Url,
  encodeBase64Url,
} from "../src/private-token/base64url.ts";
import { ChallengeParseError } from "../src/private-token/errors.ts";

test("base64url round trip across byte lengths", () => {
  for (let length = 0; length < 40; length += 1) {
    const input = new Uint8Array(length);
    for (let index = 0; index < length; index += 1) {
      input[index] = (index * 31 + 7) & 0xff;
    }
    const encoded = encodeBase64Url(input);
    assert.equal(encoded.includes("+"), false);
    assert.equal(encoded.includes("/"), false);
    assert.equal(encoded.includes("="), false);
    assert.deepEqual(decodeBase64Url(encoded), input);
  }
});

test("base64url matches RFC 7515 vectors without padding", () => {
  assert.equal(encodeBase64Url(fromHex("03")), "Aw");
  assert.equal(encodeBase64Url(fromHex("03e7")), "A-c");
  assert.equal(encodeBase64Url(fromHex("03e7c9")), "A-fJ");
  assert.deepEqual(decodeBase64Url("Aw"), fromHex("03"));
  assert.deepEqual(decodeBase64Url("A-c"), fromHex("03e7"));
  assert.deepEqual(decodeBase64Url("A-fJ"), fromHex("03e7c9"));
});

test("base64url decode tolerates standard padding", () => {
  assert.deepEqual(decodeBase64Url("Aw=="), fromHex("03"));
  assert.deepEqual(decodeBase64Url("A-c="), fromHex("03e7"));
});

test("base64url decode rejects malformed input", () => {
  assert.throws(() => decodeBase64Url("A+c"), ChallengeParseError);
  assert.throws(() => decodeBase64Url("A/c"), ChallengeParseError);
  assert.throws(() => decodeBase64Url("Aw=x"), ChallengeParseError);
  assert.throws(() => decodeBase64Url("A"), ChallengeParseError);
  assert.throws(() => decodeBase64Url("###"), ChallengeParseError);
});
