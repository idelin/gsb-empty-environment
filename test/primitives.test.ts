import assert from "node:assert/strict";
import { test } from "node:test";
import { HmacSha256, constantTimeEqual, parseLowerHex32, toLowerHex } from "../src/hmac.ts";
import { Sha256, sha256 } from "../src/sha256.ts";

const encoder = new TextEncoder();
const bytes = (value: string): Uint8Array => encoder.encode(value);
const hex = (value: Uint8Array): string => toLowerHex(value);

test("SHA-256 matches FIPS 180-4 known vectors", () => {
  assert.equal(
    hex(sha256(bytes(""))),
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  );
  assert.equal(
    hex(sha256(bytes("abc"))),
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  );
  assert.equal(
    hex(sha256(bytes("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"))),
    "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
  );
});

test("SHA-256 streaming is identical regardless of chunk boundaries", () => {
  const payload = new Uint8Array(300);
  for (let index = 0; index < payload.length; index += 1) {
    payload[index] = (index * 37 + 11) & 0xff;
  }
  const expected = hex(sha256(payload));

  for (const chunkSize of [1, 2, 3, 7, 31, 55, 63, 64, 65, 127, 256]) {
    const stream = new Sha256();
    for (let offset = 0; offset < payload.length; offset += chunkSize) {
      stream.update(payload.subarray(offset, Math.min(offset + chunkSize, payload.length)));
    }
    assert.equal(hex(stream.digest()), expected, `chunk size ${chunkSize}`);
  }
});

test("SHA-256 agrees with the platform Web Crypto implementation", async () => {
  const payload = bytes("the quick brown fox jumps over the lazy dog".repeat(50));
  const platformDigest = await crypto.subtle.digest("SHA-256", payload);
  assert.deepEqual(sha256(payload), new Uint8Array(platformDigest));
});

test("HMAC-SHA256 matches RFC 4231 test vectors", () => {
  assert.equal(
    hex(new HmacSha256(new Uint8Array(20).fill(0x0b)).update(bytes("Hi There")).digest()),
    "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
  );

  assert.equal(
    hex(new HmacSha256(bytes("Jefe")).update(bytes("what do ya want for nothing?")).digest()),
    "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
  );
});

test("HMAC-SHA256 handles long keys and chunked updates like Web Crypto", async () => {
  const longKey = new Uint8Array(120);
  for (let index = 0; index < longKey.length; index += 1) {
    longKey[index] = index & 0xff;
  }
  const payload = new Uint8Array(1000);
  for (let index = 0; index < payload.length; index += 1) {
    payload[index] = (index * 7 + 3) & 0xff;
  }

  const platformMac = new Uint8Array(
    await crypto.subtle.sign(
      "HMAC",
      await crypto.subtle.importKey(
        "raw",
        longKey,
        { name: "HMAC", hash: "SHA-256" },
        false,
        ["sign"],
      ),
      payload,
    ),
  );

  for (const chunkSize of [1, 33, 64, 100, 500, 1000]) {
    const hmac = new HmacSha256(longKey);
    for (let offset = 0; offset < payload.length; offset += chunkSize) {
      hmac.update(payload.subarray(offset, Math.min(offset + chunkSize, payload.length)));
    }
    assert.deepEqual(hmac.digest(), platformMac, `chunk size ${chunkSize}`);
  }
});

test("parseLowerHex32 accepts only exactly 64 lowercase hex chars", () => {
  const valid = "a".repeat(64);
  assert.deepEqual(parseLowerHex32(valid), new Uint8Array(32).fill(0xaa));
  assert.equal(parseLowerHex32("a".repeat(63)), null);
  assert.equal(parseLowerHex32("a".repeat(65)), null);
  assert.equal(parseLowerHex32("A".repeat(64)), null);
  assert.equal(parseLowerHex32(`${"0".repeat(63)}g`), null);
  assert.equal(parseLowerHex32(""), null);
});

test("constantTimeEqual compares all bytes without early length success", () => {
  assert.equal(constantTimeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 3])), true);
  assert.equal(constantTimeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 4])), false);
  assert.equal(constantTimeEqual(new Uint8Array([1, 2]), new Uint8Array([1, 2, 3])), false);
  assert.equal(constantTimeEqual(new Uint8Array(0), new Uint8Array(0)), true);
});
