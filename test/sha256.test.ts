import { test } from "node:test";
import assert from "node:assert/strict";
import { sha256 } from "../src/sha256.ts";

const hex = (bytes: Uint8Array): string =>
  [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");

const utf8 = (s: string): Uint8Array => new TextEncoder().encode(s);

test("SHA-256 标准测试向量（FIPS 180-4 示例）", () => {
  assert.equal(
    hex(sha256(new Uint8Array(0))),
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  );
  assert.equal(
    hex(sha256(utf8("abc"))),
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  );
  assert.equal(
    hex(sha256(utf8("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"))),
    "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
  );
  assert.equal(
    hex(sha256(utf8("The quick brown fox jumps over the lazy dog"))),
    "d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592",
  );
});

test("SHA-256 填充边界（55/56/64 字节）", () => {
  // 与 Node 无关的公开向量：NIST 示例中 1,000,000 个 'a'
  const million = new Uint8Array(1000000).fill(0x61);
  assert.equal(
    hex(sha256(million)),
    "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0",
  );
});
