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

function setup(backupEligible: boolean, backupState = false) {
  const key = generateKey("es256");
  const credentialStore = new InMemoryCredentialStore();
  const challengeStore = new InMemoryChallengeStore();
  credentialStore.save(
    makeRecord({ key, algorithm: "es256", signCount: 10, backupEligible, backupState }),
  );
  return { key, credentialStore, challengeStore };
}

describe("backup eligibility / backup state machine", () => {
  it("rejects the illegal BE=0 && BS=1 combination without touching the record", async () => {
    const harness = setup(false, false);
    const challenge = randomBytes(16);
    harness.challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key: harness.key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      flags: { up: true, uv: true, be: false, bs: true },
    });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID } satisfies AssertionInput,
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: harness.challengeStore,
        credentialStore: harness.credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "BACKUP_FLAGS_INVALID",
    );
    const stored = harness.credentialStore.get(CREDENTIAL_ID);
    assert.equal(stored?.locked, false);
    assert.equal(stored?.signCount, 10);
  });

  it("hard rejects BE transitioning false -> true and locks the credential", async () => {
    const harness = setup(false, false);
    const challenge = randomBytes(16);
    harness.challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key: harness.key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      flags: { up: true, uv: true, be: true, bs: true },
    });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID } satisfies AssertionInput,
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: harness.challengeStore,
        credentialStore: harness.credentialStore,
      }),
      (error: unknown) =>
        (error as WebAuthnVerificationError).code === "BACKUP_ELIGIBILITY_TRANSITION",
    );
    const stored = harness.credentialStore.get(CREDENTIAL_ID);
    assert.equal(stored?.locked, true);
    assert.match(stored.lockedReason ?? "", /Backup Eligibility/);
  });

  it("rejects BE=1 synced passkeys under a device-bound-only tenant policy", async () => {
    const harness = setup(true, true);
    const challenge = randomBytes(16);
    harness.challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key: harness.key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      flags: { up: true, uv: true, be: true, bs: true },
    });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID } satisfies AssertionInput,
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: harness.challengeStore,
        credentialStore: harness.credentialStore,
        requireDeviceBoundCredential: true,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "DEVICE_BOUND_POLICY",
    );
  });

  it("accepts a registered multi-device credential with BE=1/BS=1 and tracks BS changes", async () => {
    const harness = setup(true, false);
    const challenge = randomBytes(16);
    harness.challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key: harness.key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      flags: { up: true, uv: true, be: true, bs: true },
    });

    const result = await verifyAssertion({
      assertion: { ...signed, credentialId: CREDENTIAL_ID } satisfies AssertionInput,
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: harness.challengeStore,
      credentialStore: harness.credentialStore,
    });
    assert.equal(result.backupEligible, true);
    assert.equal(result.backupState, true);
    assert.equal(harness.credentialStore.get(CREDENTIAL_ID)?.backupState, true);
  });

  it("accepts a stable device-bound credential BE=0/BS=0", async () => {
    const harness = setup(false, false);
    const challenge = randomBytes(16);
    harness.challengeStore.issue(challenge);
    const signed = makeSignedAssertion({
      key: harness.key,
      algorithm: "es256",
      challenge,
      signCount: 11,
      flags: { up: true, uv: true, be: false, bs: false },
    });

    const result = await verifyAssertion({
      assertion: { ...signed, credentialId: CREDENTIAL_ID } satisfies AssertionInput,
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: harness.challengeStore,
      credentialStore: harness.credentialStore,
      requireDeviceBoundCredential: true,
    });
    assert.equal(result.backupEligible, false);
  });
});
