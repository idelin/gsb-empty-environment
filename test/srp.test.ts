import assert from "node:assert/strict";
import test from "node:test";

import {
  DEFAULT_SRP_HASH,
  RFC5054_3072,
  RFC5054_3072_G,
  RFC5054_3072_N,
  SrpError,
  SrpServer,
  computeM1,
  computeM2,
  computeMultiplier,
  computeSessionKey,
  computeU,
  computeX,
  constantTimeEqual,
  deriveVerifier,
  generateVerifierRecord,
  modPow,
} from "../srp.ts";

const username = "alice";
const password = "correct horse battery staple";
const salt = Buffer.from("0102030405060708090a0b0c0d0e0f10", "hex");
const a = BigInt("0x" + "a".repeat(64));
const b = BigInt("0x" + "b".repeat(64));

function assertSrpCode(code: string) {
  return (error: unknown): boolean =>
    error instanceof SrpError && error.code === code;
}

function makeRecord(recordPassword = password) {
  return generateVerifierRecord(username, recordPassword, { salt });
}

function clientProof(
  record: ReturnType<typeof makeRecord>,
  clientA: bigint,
  serverB: bigint,
  clientPassword = password,
) {
  const x = computeX(username, clientPassword, record.salt);
  const k = computeMultiplier();
  const u = computeU(clientA, serverB);
  const base =
    (serverB - k * modPow(RFC5054_3072_G, x, RFC5054_3072_N)) %
    RFC5054_3072_N;
  const positiveBase =
    base < 0n ? base + RFC5054_3072_N : base;
  const S = modPow(
    positiveBase,
    a + u * x,
    RFC5054_3072_N,
  );
  const K = computeSessionKey(S);
  const M1 = computeM1(username, record.salt, clientA, serverB, K);
  return { M1, K };
}

test("uses the RFC 5054 3072-bit group", () => {
  assert.equal(RFC5054_3072.name, "RFC 5054 3072-bit group");
  assert.equal(RFC5054_3072.bytes, 384);
  assert.equal(RFC5054_3072_G, 5n);
  assert.equal(RFC5054_3072_N.toString(16).length, 768);
  assert.equal(DEFAULT_SRP_HASH, "sha256");
});

test("rejects A = 0, N, and 2N before deriving a session key", () => {
  const record = makeRecord();
  for (const maliciousA of [0n, RFC5054_3072_N, 2n * RFC5054_3072_N]) {
    const server = new SrpServer(record, { b });
    assert.notEqual(server.B, 0n);
    assert.throws(
      () => server.challenge(maliciousA),
      assertSrpCode("INVALID_A_ZERO_MOD_N"),
    );
    assert.equal(server.expectedClientProof, null);
    assert.throws(
      () => server.sessionKeyValue,
      assertSrpCode("SRP_NOT_STARTED"),
    );
  }
});

test("rejects a zero encoded A and an out-of-range A", () => {
  const record = makeRecord();
  const zeroBytes = Buffer.alloc(RFC5054_3072.bytes);
  const server = new SrpServer(record, { b });
  assert.throws(
    () => server.start(zeroBytes),
    assertSrpCode("INVALID_A_ZERO_MOD_N"),
  );

  const otherServer = new SrpServer(record, { b });
  assert.throws(
    () => otherServer.start(RFC5054_3072_N + 1n),
    assertSrpCode("INVALID_A_RANGE"),
  );
});

test("generates a non-zero B from b and the verifier", () => {
  const record = makeRecord();
  const server = new SrpServer(record, { b });
  const k = computeMultiplier();
  const expectedB =
    (k * record.verifier + modPow(RFC5054_3072_G, b, RFC5054_3072_N)) %
    RFC5054_3072_N;

  assert.equal(server.B, expectedB);
  assert.notEqual(server.B, 0n);
  assert.ok(server.B > 0n && server.B < RFC5054_3072_N);
  assert.equal(server.Bbytes.length, 384);
  assert.equal(server.Bbytes.toString("hex"), server.B.toString(16).padStart(768, "0"));
});

test("generates fresh non-zero B values when b is not injected", () => {
  const record = makeRecord();
  const first = new SrpServer(record);
  const second = new SrpServer(record);

  assert.notEqual(first.B, 0n);
  assert.notEqual(second.B, 0n);
  assert.notEqual(first.B, second.B);
});

test("accepts a legal fixed vector and returns the expected M1/M2", () => {
  const record = makeRecord();
  const server = new SrpServer(record, { b });
  const A = modPow(RFC5054_3072_G, a, RFC5054_3072_N);
  const challenge = server.challenge(A);
  const proof = clientProof(record, A, challenge.B);

  assert.equal(
    proof.M1.toString("hex"),
    "c4bbf16f896f71afb089ba004bd187da058e217a0b9db8f70391b73cfe8371a4",
  );
  assert.equal(
    proof.K.toString("hex"),
    "22561ab30a58ad4ebbbbf9172019b15289165bd852726de294ec79cf8bd07b08",
  );

  const result = server.verifyClientProof(proof.M1);
  assert.ok(result);
  assert.ok(result.K.equals(proof.K));
  assert.equal(
    result.M2.toString("hex"),
    "8631113e6623d56167c8939662a6351237b912e9767f8e67b9d4ceda9c5b689a",
  );
  assert.deepEqual(server.getM2(), result.M2);
  assert.ok(server.verifyM1(proof.M1));
});

test("rejects a proof made with the wrong password", () => {
  const record = makeRecord();
  const server = new SrpServer(record, { b });
  const A = modPow(RFC5054_3072_G, a, RFC5054_3072_N);
  const challenge = server.challenge(A);
  const badProof = clientProof(record, A, challenge.B, "wrong password");

  assert.equal(server.verifyClientProof(badProof.M1), null);
  assert.equal(server.verifyM1(badProof.M1), false);
  assert.throws(
    () => server.getM2(),
    assertSrpCode("SRP_NOT_VERIFIED"),
  );
});

test("M1 and M2 are bound to username, salt, A, B, and K", () => {
  const A = modPow(RFC5054_3072_G, a, RFC5054_3072_N);
  const B = A + 1n;
  const K = Buffer.from("00112233445566778899aabbccddeeff", "hex");
  const baselineM1 = computeM1(username, salt, A, B, K);

  assert.notEqual(
    computeM1("bob", salt, A, B, K).toString("hex"),
    baselineM1.toString("hex"),
  );
  assert.notEqual(
    computeM1(username, Buffer.from("ffeeddccbbaa99887766554433221100", "hex"), A, B, K).toString("hex"),
    baselineM1.toString("hex"),
  );
  assert.notEqual(
    computeM1(username, salt, A + 2n, B, K).toString("hex"),
    baselineM1.toString("hex"),
  );
  assert.notEqual(
    computeM1(username, salt, A, B + 2n, K).toString("hex"),
    baselineM1.toString("hex"),
  );
  assert.notEqual(
    computeM1(username, salt, A, B, Buffer.alloc(16)).toString("hex"),
    baselineM1.toString("hex"),
  );

  assert.notEqual(
    computeM2(A + 1n, baselineM1, K).toString("hex"),
    computeM2(A, baselineM1, K).toString("hex"),
  );
  assert.notEqual(
    computeM2(A, computeM1("bob", salt, A, B, K), K).toString("hex"),
    computeM2(A, baselineM1, K).toString("hex"),
  );
});

test("constant-time comparison handles equals, mismatches, and lengths", () => {
  assert.equal(constantTimeEqual(Buffer.from([1, 2, 3]), Buffer.from([1, 2, 3])), true);
  assert.equal(constantTimeEqual(Buffer.from([1, 2, 3]), Buffer.from([1, 2, 4])), false);
  assert.equal(constantTimeEqual(Buffer.from([1, 2, 3]), Buffer.from([1, 2])), false);
});
