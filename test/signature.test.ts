import assert from "node:assert/strict";
import { test } from "node:test";
import {
  parseSignature,
  SECP256K1_HALF_N,
  SignatureVerificationError,
  verifyTypedData,
  VerificationErrorCode,
} from "../src/index.js";
import { bytesToHex } from "../src/bytes.js";
import { keccak256 } from "./support/keccak.js";
import {
  recoverAddress,
  signDigest,
  signatureToBytes,
  toHighS,
} from "./support/secp256k1.js";
import {
  AUTHORIZATION_TYPES,
  buildDomain,
  buildMessage,
  digestFor,
  makeSigner,
  signAuthorization,
} from "./fixtures.js";

test("parseSignature 接受 27/28 并归一为 0/1，拒绝其它 v", () => {
  const signer = makeSigner(0xd00dn);
  const message = buildMessage(signer);
  const digest = digestFor(buildDomain(), message);
  const raw = signatureToBytes(signDigest(digest, signer.privateKey));

  const parsed = parseSignature(raw);
  assert.ok(parsed.recoveryId === 0 || parsed.recoveryId === 1);
  assert.equal(parsed.compact.length, 65);
  assert.equal(parsed.compact[64], parsed.recoveryId);

  const badV = new Uint8Array(raw);
  badV[64] = 29;
  assert.throws(
    () => parseSignature(badV),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.INVALID_V,
  );
});

test("parseSignature 对高 s 抛 HIGH_S", () => {
  const signer = makeSigner(0xd00en);
  const digest = digestFor(buildDomain(), buildMessage(signer));
  const high = signatureToBytes(toHighS(signDigest(digest, signer.privateKey)));
  assert.ok(BigInt(bytesToHex(high.slice(32, 64))) > SECP256K1_HALF_N);
  assert.throws(
    () => parseSignature(high),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.HIGH_S,
  );
});

test("verifyTypedData 对合法签名通过，对错误签名者抛 SIGNER_MISMATCH", async () => {
  const signer = makeSigner(0xd00fn);
  const other = makeSigner(0xd010n);
  const domain = buildDomain();
  const message = buildMessage(signer);
  const signature = signAuthorization(signer, domain, message);

  await assert.doesNotReject(() =>
    verifyTypedData(
      { keccak256, recoverAddress },
      {
        domain,
        types: AUTHORIZATION_TYPES,
        primaryType: "Authorization",
        message,
        expectedSigner: signer.address,
        signature,
      },
    ),
  );

  await assert.rejects(
    () =>
      verifyTypedData(
        { keccak256, recoverAddress },
        {
          domain,
          types: AUTHORIZATION_TYPES,
          primaryType: "Authorization",
          message,
          expectedSigner: other.address,
          signature,
        },
      ),
    (error: unknown) =>
      error instanceof SignatureVerificationError &&
      error.code === VerificationErrorCode.SIGNER_MISMATCH,
  );
});
