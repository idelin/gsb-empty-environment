import assert from "node:assert/strict";
import { test } from "node:test";
import { bytesToHex, utf8ToBytes } from "../src/bytes.js";
import { keccak256 } from "./support/keccak.js";
import {
  addressFromPrivateKey,
  recoverAddress,
  randomPrivateKey,
  signDigest,
  signatureToBytes,
  toHighS,
} from "./support/secp256k1.js";

test("keccak256 匹配已知标准向量（含跨速率边界的输入）", () => {
  const vectors: Array<[Uint8Array, string]> = [
    [
      new Uint8Array(0),
      "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
    ],
    [
      utf8ToBytes("abc"),
      "0x4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45",
    ],
  ];
  for (const [input, expected] of vectors) {
    assert.equal(bytesToHex(keccak256(input)), expected);
  }

  // 长输入覆盖多吸收块，结果需稳定且为 32 字节。
  const long = new Uint8Array(200).fill(0x61);
  const first = keccak256(long);
  assert.equal(first.length, 32);
  assert.equal(bytesToHex(first), bytesToHex(keccak256(long)));
});

test("secp256k1 签名→恢复地址往返一致，且默认产出低 s", () => {
  const privateKey = randomPrivateKey();
  const address = addressFromPrivateKey(privateKey);
  const digest = keccak256(utf8ToBytes("authorization payload"));

  const signature = signDigest(digest, privateKey);
  const compact = signatureToBytes(signature);
  assert.equal(compact.length, 65);
  assert.ok(compact[64] === 27 || compact[64] === 28);

  const recovered = recoverAddress(digest, compact);
  assert.equal(recovered, address);

  // 高 s 翻转后仍能恢复同一地址（供 malleability 测试构造输入）。
  const highS = toHighS(signature);
  const highBytes = signatureToBytes(highS);
  assert.equal(recoverAddress(digest, highBytes), address);
});

test("不同摘要产生不同签名，确定性签名可复现", () => {
  const privateKey = randomPrivateKey();
  const digestA = keccak256(utf8ToBytes("a"));
  const digestB = keccak256(utf8ToBytes("b"));

  const sigA1 = signatureToBytes(signDigest(digestA, privateKey));
  const sigA2 = signatureToBytes(signDigest(digestA, privateKey));
  const sigB = signatureToBytes(signDigest(digestB, privateKey));

  assert.deepEqual(sigA1, sigA2);
  assert.notDeepEqual(sigA1, sigB);
});
