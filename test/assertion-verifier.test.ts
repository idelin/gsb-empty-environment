import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import {
  InMemoryChallengeStore,
  InMemoryCredentialStore,
  verifyAssertion,
  WebAuthnVerificationError,
  type AssertionInput,
} from "../src/index.ts";
import {
  CREDENTIAL_ID,
  ORIGIN,
  RP_ID,
  generateKey,
  makeRecord,
  makeSignedAssertion,
} from "./helpers.ts";

type Algorithm = "es256" | "ed25519" | "rs256";

function buildHarness(algorithm: Algorithm) {
  const key = generateKey(algorithm);
  const credentialStore = new InMemoryCredentialStore();
  const challengeStore = new InMemoryChallengeStore();
  credentialStore.save(makeRecord({ key, algorithm, signCount: 10 }));
  return { key, credentialStore, challengeStore };
}

async function runSuccess(algorithm: Algorithm): Promise<void> {
  const { key, credentialStore, challengeStore } = buildHarness(algorithm);
  const challenge = randomBytes(32);
  challengeStore.issue(challenge);
  const signed = makeSignedAssertion({
    key,
    algorithm,
    challenge,
    signCount: 11,
    flags: { up: true, uv: true },
  });
  const assertion: AssertionInput = {
    authenticatorData: signed.authenticatorData,
    clientDataJSON: signed.clientDataJSON,
    signature: signed.signature,
    credentialId: CREDENTIAL_ID,
  };

  const result = await verifyAssertion({
    assertion,
    rpId: RP_ID,
    expectedOrigin: ORIGIN,
    challenge: challengeStore,
    credentialStore,
  });

  assert.equal(result.signCount, 11);
  assert.equal(result.previousSignCount, 10);
  const stored = credentialStore.get(CREDENTIAL_ID);
  assert.ok(stored);
  assert.equal(stored.signCount, 11);
  assert.equal(challengeStore.size(), 0);
}

describe("verifyAssertion happy path", () => {
  it("accepts ES256 / P-256 assertions and persists the counter", async () => {
    await runSuccess("es256");
  });

  it("accepts Ed25519 assertions", async () => {
    await runSuccess("ed25519");
  });

  it("accepts RS256 assertions", async () => {
    await runSuccess("rs256");
  });

  it("accepts a base64url-string assertion envelope", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 12 });

    const result = await verifyAssertion({
      assertion: {
        authenticatorData: Buffer.from(signed.authenticatorData).toString("base64url"),
        clientDataJSON: Buffer.from(signed.clientDataJSON).toString("base64url"),
        signature: Buffer.from(signed.signature).toString("base64url"),
        credentialId: CREDENTIAL_ID,
      },
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: challengeStore,
      credentialStore,
    });
    assert.equal(result.signCount, 12);
  });
});

describe("structural and binding checks", () => {
  it("rejects authenticator data shorter than 37 bytes", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 11 });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, authenticatorData: signed.authenticatorData.slice(0, 36), credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "AUTHENTICATOR_DATA_MALFORMED",
    );
    assert.equal(challengeStore.size(), 1, "challenge must remain unconsumed when parsing fails");
  });

  it("rejects mismatched rpIdHash", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      rpId: "attacker.example",
    });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "RP_ID_MISMATCH",
    );
  });

  it("rejects non webauthn.get client data type", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      type: "webauthn.create",
    });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CLIENT_TYPE_MISMATCH",
    );
  });

  it("requires exact origin equality and supports an allow-list", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      origin: "https://evil.example",
    });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "ORIGIN_MISMATCH",
    );

    const secondChallenge = randomBytes(16);
    challengeStore.issue(secondChallenge);
    const second = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge: secondChallenge,
      signCount: 12,
      origin: "https://evil.example",
    });
    const result = await verifyAssertion({
      assertion: { ...second, credentialId: CREDENTIAL_ID },
      rpId: RP_ID,
      expectedOrigin: [ORIGIN, "https://evil.example"],
      challenge: challengeStore,
      credentialStore,
    });
    assert.equal(result.signCount, 12);
  });

  it("rejects UP=0 and policy-required UV=0", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const noUp = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      flags: { up: false, uv: true },
    });
    await assert.rejects(
      verifyAssertion({
        assertion: { ...noUp, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "USER_PRESENCE_REQUIRED",
    );

    const challenge2 = randomBytes(16);
    challengeStore.issue(challenge2);
    const noUv = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge: challenge2,
      signCount: 12,
      flags: { up: true, uv: false },
    });
    await assert.rejects(
      verifyAssertion({
        assertion: { ...noUv, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "USER_VERIFICATION_REQUIRED",
    );

    const challenge3 = randomBytes(16);
    challengeStore.issue(challenge3);
    const uvOptional = makeSignedAssertion({
      key,
      algorithm: "es256",
      challenge: challenge3,
      signCount: 13,
      flags: { up: true, uv: false },
    });
    const result = await verifyAssertion({
      assertion: { ...uvOptional, credentialId: CREDENTIAL_ID },
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: challengeStore,
      credentialStore,
      requireUserVerification: false,
    });
    assert.equal(result.signCount, 13);
  });

  it("rejects unknown credential ids and locked credentials", async () => {
    const { key, credentialStore, challengeStore } = buildHarness("es256");
    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 11 });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: randomBytes(16) },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CREDENTIAL_NOT_FOUND",
    );

    const record = credentialStore.get(CREDENTIAL_ID);
    assert.ok(record);
    credentialStore.save({ ...record, locked: true, lockedReason: "test" });
    const challenge2 = randomBytes(16);
    challengeStore.issue(challenge2);
    const second = makeSignedAssertion({ key, algorithm: "es256", challenge: challenge2, signCount: 12 });
    await assert.rejects(
      verifyAssertion({
        assertion: { ...second, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CREDENTIAL_LOCKED",
    );
  });
});
