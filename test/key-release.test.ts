import {
  generateKeyPairSync,
  privateDecrypt,
  constants,
} from "node:crypto";
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  KeyReleaseService,
  buildReportData,
} from "../index.ts";
import type {
  AttestationToken,
  JsonObject,
  KeyStore,
  PolicyNode,
  RuntimeData,
  TokenVerifier,
} from "../src/types.ts";
import {
  KeyWrappingError,
  PolicyNotSatisfiedError,
  RuntimeDataBindingError,
  TokenVerificationError,
} from "../src/errors.ts";

const { publicKey, privateKey } = generateKeyPairSync("rsa", {
  modulusLength: 2048,
});
const publicPem = publicKey.export({ type: "spki", format: "pem" }).toString();

const secret = Buffer.from("master-secret-key-32-bytes-long!!!");
const secretBytes = secret.buffer.slice(
  secret.byteOffset,
  secret.byteOffset + secret.byteLength,
) as ArrayBuffer;

const keyStore: KeyStore = {
  async get(keyId: string) {
    if (keyId === "kms-key-1") {
      return { keyId, keyMaterial: secretBytes };
    }
    return undefined;
  },
};

const policy: PolicyNode = {
  allOf: [
    { claim: "instance_id", equals: "vm-A" },
    { claim: "tcb.debug", equals: false },
    { claim: "tcb.version", gte: 12 },
    {
      anyOf: [
        { claim: "measured_boot", equals: true },
        { claim: "policy_ids", in: ["policy-1", "policy-2"] },
      ],
    },
  ],
};

function runtimeDataFor(pem: string): RuntimeData {
  return {
    public: pem,
    alg: "RSA-OAEP-256",
    nonce: "request-nonce",
  };
}

function tokenClaims(overrides: JsonObject = {}): JsonObject {
  const runtimeData =
    (overrides.__runtimeData as RuntimeData | undefined) ??
    runtimeDataFor(publicPem);
  const clean = { ...overrides };
  delete clean.__runtimeData;
  return {
    instance_id: "vm-A",
    tcb: { debug: false, version: 12 },
    measured_boot: true,
    policy_ids: ["policy-1"],
    "runtime-data": runtimeData,
    report_data: buildReportData(runtimeData, "SHA-384"),
    ...clean,
  };
}

function verifierFor(claims: JsonObject): TokenVerifier {
  return async (): Promise<AttestationToken> => ({
    claims,
    rawToken: "signed-token",
  });
}

function service(
  claims: JsonObject,
  verifier?: TokenVerifier,
): KeyReleaseService {
  return new KeyReleaseService(
    policy,
    {
      verifyToken: verifier ?? verifierFor(claims),
      keyStore,
    },
    { runtimeDataHash: "SHA-384" },
  );
}

test("happy path: key is wrapped to the attested runtime-data public key", async () => {
  const claims = tokenClaims();
  const result = await service(claims).releaseKey({
    token: "signed-token",
    keyId: "kms-key-1",
  });

  assert.equal(result.keyId, "kms-key-1");
  assert.equal(result.wrappingAlgorithm, "RSA-OAEP-256");
  assert.equal(result.wrappedKeyHashAlgorithm, "SHA-384");

  const decrypted = privateDecrypt(
    {
      key: privateKey,
      padding: constants.RSA_PKCS1_OAEP_PADDING,
      oaepHash: "sha256",
    },
    result.wrappedKey,
  );
  assert.deepEqual(decrypted, secret);
});

test("host key-swap: another public key in runtime-data fails report_data binding", async () => {
  const attacker = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const attackerPem = attacker.publicKey
    .export({ type: "spki", format: "pem" })
    .toString();

  // Attacker-controlled runtime-data, but report_data still quotes the
  // original attested data hash.
  const claims = tokenClaims({
    "runtime-data": runtimeDataFor(attackerPem),
  });
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "t", keyId: "kms-key-1" }),
    RuntimeDataBindingError,
  );
});

test("stale TCB below the lower bound blocks release", async () => {
  const claims = tokenClaims({ tcb: { debug: false, version: 11 } });
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "t", keyId: "kms-key-1" }),
    (error: unknown) =>
      error instanceof PolicyNotSatisfiedError &&
      /gte 12/.test(error.message),
  );
});

test("type confusion: string \"false\" for tcb.debug blocks release", async () => {
  const claims = tokenClaims({ tcb: { debug: "false", version: 12 } });
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "t", keyId: "kms-key-1" }),
    (error: unknown) =>
      error instanceof PolicyNotSatisfiedError &&
      /string.*!==.*boolean/.test(error.message),
  );
});

test("missing claim blocks release", async () => {
  const claims = tokenClaims();
  delete claims.measured_boot;
  // anyOf still passes via policy_ids; instead remove a required allOf claim.
  delete claims.instance_id;
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "t", keyId: "kms-key-1" }),
    /instance_id.*missing/,
  );
});

test("debug VM token is refused even with current TCB", async () => {
  const claims = tokenClaims({ tcb: { debug: true, version: 99 } });
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "t", keyId: "kms-key-1" }),
    PolicyNotSatisfiedError,
  );
});

test("replayed token from another VM fails the instance_id rule", async () => {
  const claims = tokenClaims({ instance_id: "vm-VICTIM" });
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "replayed", keyId: "kms-key-1" }),
    /instance_id/,
  );
});

test("token signature failure from the injected verifier blocks release", async () => {
  const failingVerifier: TokenVerifier = async () => {
    throw new Error("signature does not verify against attestation authority");
  };
  await assert.rejects(
    () =>
      service(tokenClaims(), failingVerifier).releaseKey({
        token: "forged",
        keyId: "kms-key-1",
      }),
    TokenVerificationError,
  );
});

test("nested anyOf: measured_boot false still passes via policy_ids", async () => {
  const claims = tokenClaims({
    measured_boot: false,
    policy_ids: ["policy-2"],
  });
  const result = await service(claims).releaseKey({
    token: "t",
    keyId: "kms-key-1",
  });
  assert.equal(result.wrappingAlgorithm, "RSA-OAEP-256");
});

test("unknown key id is reported as a wrapping error", async () => {
  await assert.rejects(
    () =>
      service(tokenClaims()).releaseKey({
        token: "t",
        keyId: "does-not-exist",
      }),
    KeyWrappingError,
  );
});

test("non-RSA wrapping key in runtime-data is rejected", async () => {
  const ed = generateKeyPairSync("ed25519");
  const edPem = ed.publicKey
    .export({ type: "spki", format: "pem" })
    .toString();
  const runtimeData = runtimeDataFor(edPem);
  const claims = tokenClaims({
    __runtimeData: runtimeData,
    "runtime-data": runtimeData,
    report_data: buildReportData(runtimeData, "SHA-384"),
  });
  await assert.rejects(
    () =>
      service(claims).releaseKey({ token: "t", keyId: "kms-key-1" }),
    /requires an RSA public key/,
  );
});
