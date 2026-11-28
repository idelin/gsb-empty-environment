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

function setup() {
  const key = generateKey("es256");
  const credentialStore = new InMemoryCredentialStore();
  const challengeStore = new InMemoryChallengeStore();
  credentialStore.save(makeRecord({ key, algorithm: "es256", signCount: 10 }));
  return { key, credentialStore, challengeStore };
}

function buildAssertion(
  harness: ReturnType<typeof setup>,
  challenge: Uint8Array,
  overrides: { crossOrigin?: boolean; origin?: string; signCount?: number } = {},
): AssertionInput {
  const signed = makeSignedAssertion({
    key: harness.key,
    algorithm: "es256",
    challenge,
    signCount: overrides.signCount ?? 11,
    crossOrigin: overrides.crossOrigin,
    origin: overrides.origin,
  });
  return { ...signed, credentialId: CREDENTIAL_ID };
}

describe("one-time challenge consumption", () => {
  it("atomically consumes a challenge so a replayed assertion is rejected", async () => {
    const harness = setup();
    const challenge = randomBytes(32);
    harness.challengeStore.issue(challenge);
    const assertion = buildAssertion(harness, challenge, { signCount: 11 });

    await verifyAssertion({
      assertion,
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: harness.challengeStore,
      credentialStore: harness.credentialStore,
    });

    const replay = await verifyAssertion({
      assertion,
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: harness.challengeStore,
      credentialStore: harness.credentialStore,
    }).then(
      () => null,
      (error: unknown) => error as WebAuthnVerificationError,
    );
    assert.ok(replay);
    assert.equal(replay.code, "CHALLENGE_REPLAY");
  });

  it("allows exactly one winner across concurrent requests sharing one challenge", async () => {
    const harness = setup();
    const challenge = randomBytes(32);
    harness.challengeStore.issue(challenge);
    const assertion = buildAssertion(harness, challenge, { signCount: 12 });

    const outcomes = await Promise.allSettled(
      Array.from({ length: 8 }, () =>
        verifyAssertion({
          assertion,
          rpId: RP_ID,
          expectedOrigin: ORIGIN,
          challenge: harness.challengeStore,
          credentialStore: harness.credentialStore,
        }),
      ),
    );

    const fulfilled = outcomes.filter((outcome) => outcome.status === "fulfilled");
    const rejected = outcomes.filter((outcome) => outcome.status === "rejected");
    assert.equal(fulfilled.length, 1);
    assert.equal(rejected.length, 7);
    for (const outcome of rejected) {
      if (outcome.status === "rejected") {
        assert.equal((outcome.reason as WebAuthnVerificationError).code, "CHALLENGE_REPLAY");
      }
    }
  });

  it("keeps single-winner semantics under repeated concurrent bursts", async () => {
    for (let burst = 0; burst < 5; burst += 1) {
      const harness = setup();
      const challenge = randomBytes(32);
      harness.challengeStore.issue(challenge);
      const assertion = buildAssertion(harness, challenge, { signCount: 30 + burst });

      const outcomes = await Promise.allSettled(
        Array.from({ length: 16 }, () =>
          verifyAssertion({
            assertion,
            rpId: RP_ID,
            expectedOrigin: ORIGIN,
            challenge: harness.challengeStore,
            credentialStore: harness.credentialStore,
          }),
        ),
      );
      assert.equal(outcomes.filter((outcome) => outcome.status === "fulfilled").length, 1);
      assert.equal(harness.challengeStore.size(), 0);
    }
  });

  it("rejects an expired challenge after TTL elapses", async () => {
    let currentTime = 1_000_000;
    const clock = (): number => currentTime;
    const key = generateKey("es256");
    const credentialStore = new InMemoryCredentialStore();
    const challengeStore = new InMemoryChallengeStore({ now: clock });
    credentialStore.save(makeRecord({ key, algorithm: "es256", signCount: 10 }));

    const challenge = randomBytes(16);
    challengeStore.issue(challenge, 50);
    currentTime += 60;

    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 11 });
    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
        now: clock,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CHALLENGE_EXPIRED",
    );
  });

  it("rejects an unknown challenge without consuming the issued one", async () => {
    const harness = setup();
    const issued = randomBytes(16);
    const presented = randomBytes(16);
    harness.challengeStore.issue(issued);
    const assertion = buildAssertion(harness, presented);

    await assert.rejects(
      verifyAssertion({
        assertion,
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: harness.challengeStore,
        credentialStore: harness.credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CHALLENGE_REPLAY",
    );
    assert.equal(harness.challengeStore.size(), 1, "the issued challenge must remain available");
  });

  it("supports raw expected-challenge mode without a store", async () => {
    const harness = setup();
    const challenge = randomBytes(16);
    const assertion = buildAssertion(harness, challenge, { signCount: 14 });
    const result = await verifyAssertion({
      assertion,
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge,
      credentialStore: harness.credentialStore,
    });
    assert.equal(result.signCount, 14);

    const wrong = buildAssertion(harness, randomBytes(16), { signCount: 15 });
    await assert.rejects(
      verifyAssertion({
        assertion: wrong,
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge,
        credentialStore: harness.credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CHALLENGE_MISMATCH",
    );
  });

  it("rejects crossOrigin=true by policy and can explicitly allow it", async () => {
    const harness = setup();
    const challenge = randomBytes(16);
    harness.challengeStore.issue(challenge);
    const assertion = buildAssertion(harness, challenge, {
      crossOrigin: true,
      signCount: 16,
    });

    await assert.rejects(
      verifyAssertion({
        assertion,
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: harness.challengeStore,
        credentialStore: harness.credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CROSS_ORIGIN_FORBIDDEN",
    );

    const challenge2 = randomBytes(16);
    harness.challengeStore.issue(challenge2);
    const allowed = buildAssertion(harness, challenge2, { crossOrigin: true, signCount: 17 });
    const result = await verifyAssertion({
      assertion: allowed,
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: harness.challengeStore,
      credentialStore: harness.credentialStore,
      rejectCrossOrigin: false,
    });
    assert.equal(result.signCount, 17);
  });
});
