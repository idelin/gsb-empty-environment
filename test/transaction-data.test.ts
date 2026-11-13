import { generateKeyPairSync, sign } from "node:crypto";
import assert from "node:assert/strict";
import test from "node:test";
import {
  buildKbJwt,
  extractTransactionDataHashes,
  hashTransactionData,
  verifyPresentation,
} from "../index.ts";

const encoder = new TextEncoder();
const displayed = encoder.encode('{"to":"merchant-a","amount":"12.50"}');
const other = encoder.encode('{"to":"attacker-x","amount":"999.00"}');

const { publicKey: devicePublicKey, privateKey: devicePrivateKey } =
  generateKeyPairSync("ed25519");

test("哈希覆盖当前交易字节时允许出示（kb-jwt）", () => {
  const kbJwt = buildKbJwt([hashTransactionData(displayed)]);
  const result = verifyPresentation({ presentedTransactionData: displayed, kbJwt });
  assert.equal(result.allowed, true);
});

test("kb-jwt 签名另一份交易的哈希时拒绝出示", () => {
  const kbJwt = buildKbJwt([hashTransactionData(other)]);
  const result = verifyPresentation({ presentedTransactionData: displayed, kbJwt });
  assert.equal(result.allowed, false);
  assert.match(result.reason, /不覆盖当前交易数据字节/);
});

test("kb-jwt 缺失交易哈希声明时拒绝出示", () => {
  const header = Buffer.from('{"alg":"ES256"}').toString("base64url");
  const payload = Buffer.from("{}").toString("base64url");
  const result = verifyPresentation({
    presentedTransactionData: displayed,
    kbJwt: `${header}.${payload}.`,
  });
  assert.equal(result.allowed, false);
  assert.match(result.reason, /缺失/);
});

test("设备签名覆盖当前交易哈希时允许出示", () => {
  const signature = sign(null, Buffer.from(hashTransactionData(displayed)), devicePrivateKey);
  const result = verifyPresentation({
    presentedTransactionData: displayed,
    deviceSignature: signature,
    devicePublicKey,
  });
  assert.equal(result.allowed, true);
});

test("设备签名覆盖另一份交易哈希时拒绝出示", () => {
  const signature = sign(null, Buffer.from(hashTransactionData(other)), devicePrivateKey);
  const result = verifyPresentation({
    presentedTransactionData: displayed,
    deviceSignature: signature,
    devicePublicKey,
  });
  assert.equal(result.allowed, false);
  assert.match(result.reason, /设备签名/);
});

test("既无 kb-jwt 又无设备签名时拒绝出示", () => {
  const result = verifyPresentation({ presentedTransactionData: displayed });
  assert.equal(result.allowed, false);
  assert.match(result.reason, /未提供/);
});

test("kb-jwt 哈希算法不符时拒绝出示", () => {
  const kbJwt = buildKbJwt([hashTransactionData(displayed, "sha-512")], "sha-512");
  const result = verifyPresentation({ presentedTransactionData: displayed, kbJwt });
  assert.equal(result.allowed, false);
  assert.match(result.reason, /算法不符/);
});

test("extractTransactionDataHashes 解析声明", () => {
  const hash = hashTransactionData(displayed);
  const kbJwt = buildKbJwt([hash]);
  const extracted = extractTransactionDataHashes(kbJwt);
  assert.ok(extracted);
  assert.equal(extracted.alg, "sha-256");
  assert.deepEqual(extracted.hashes[0], hash);
});
