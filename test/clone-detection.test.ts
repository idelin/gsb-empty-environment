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

function setup(signCount: number) {
  const key = generateKey("es256");
  const credentialStore = new InMemoryCredentialStore();
  const challengeStore = new InMemoryChallengeStore();
  credentialStore.save(makeRecord({ key, algorithm: "es256", signCount }));
  return { key, credentialStore, challengeStore };
}

async function attempt(
  harness: ReturnType<typeof setup>,
  observedSignCount: number,
  extra: { requireUserVerification?: boolean } = {},
): Promise<unknown> {
  const challenge = randomBytes(16);
  harness.challengeStore.issue(challenge);
  const signed = makeSignedAssertion({
    key: harness.key,
    algorithm: "es256",
    challenge,
    signCount: observedSignCount,
  });
  return verifyAssertion({
    assertion: { ...signed, credentialId: CREDENTIAL_ID } satisfies AssertionInput,
    rpId: RP_ID,
    expectedOrigin: ORIGIN,
    challenge: harness.challengeStore,
    credentialStore: harness.credentialStore,
    ...extra,
  });
}

describe("signCount clone detection state machine", () => {
  it("locks the credential when the counter regresses", async () => {
    const harness = setup(100);
    await assert.rejects(attempt(harness, 42), (error: unknown) => {
      const typed = error as WebAuthnVerificationError;
      return typed.code === "CLONE_DETECTED";
    });
    const stored = harness.credentialStore.get(CREDENTIAL_ID);
    assert.ok(stored?.locked);
    assert.match(stored.lockedReason ?? "", /signCount/);

    await assert.rejects(attempt(harness, 200), (error: unknown) => {
      return (error as WebAuthnVerificationError).code === "CREDENTIAL_LOCKED";
    });
  });

  it("locks the credential when the counter stagnates at a non-zero value", async () => {
    const harness = setup(50);
    await assert.rejects(attempt(harness, 50), (error: unknown) => {
      return (error as WebAuthnVerificationError).code === "CLONE_DETECTED";
    });
    assert.equal(harness.credentialStore.get(CREDENTIAL_ID)?.locked, true);
  });

  it("allows zero/zero counters (spec-level counter opt-out)", async () => {
    const harness = setup(0);
    const result = await attempt(harness, 0);
    assert.equal((result as { signCount: number }).signCount, 0);
    assert.equal(harness.credentialStore.get(CREDENTIAL_ID)?.locked, false);
  });

  it("flags a clone when stored=0 but the authenticator returns zero after having counted", async () => {
    const harness = setup(25);
    await assert.rejects(attempt(harness, 0), (error: unknown) => {
      return (error as WebAuthnVerificationError).code === "CLONE_DETECTED";
    });
    assert.equal(harness.credentialStore.get(CREDENTIAL_ID)?.locked, true);
  });

  it("accepts a strictly increasing counter and persists it", async () => {
    const harness = setup(10);
    const result = await attempt(harness, 99);
    assert.equal((result as { signCount: number }).signCount, 99);
    assert.equal(harness.credentialStore.get(CREDENTIAL_ID)?.signCount, 99);

    const next = await attempt(harness, 100);
    assert.equal((next as { signCount: number }).signCount, 100);
  });

  it("treats uint32 wraparound to zero as a clone event", async () => {
    const harness = setup(0xffffffff);
    await assert.rejects(attempt(harness, 0), (error: unknown) => {
      return (error as WebAuthnVerificationError).code === "CLONE_DETECTED";
    });
  });
});
