import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import {
  InMemoryChallengeStore,
  InMemoryCredentialStore,
  SignatureError,
  WebAuthnVerificationError,
  assertLowSP256,
  parseStrictDerEcdsaSignature,
  verifyAssertion,
} from "../src/index.ts";
import {
  CREDENTIAL_ID,
  ORIGIN,
  RP_ID,
  ecdsaHighSVariant,
  generateKey,
  makeRecord,
  makeSignedAssertion,
} from "./helpers.ts";

describe("ECDSA strict DER and low-S enforcement", () => {
  it("parses a canonical low-S signature", () => {
    const key = generateKey("es256");
    const challenge = randomBytes(16);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 1 });
    const parsed = parseStrictDerEcdsaSignature(signed.signature);
    assert.doesNotThrow(() => assertLowSP256(parsed));
  });

  it("rejects high-S signatures", () => {
    const key = generateKey("es256");
    const challenge = randomBytes(16);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 1 });
    const highS = ecdsaHighSVariant(signed.signature);
    const parsed = parseStrictDerEcdsaSignature(highS);
    assert.throws(() => assertLowSP256(parsed), SignatureError);
  });

  it("rejects trailing bytes after the DER sequence", () => {
    const key = generateKey("es256");
    const challenge = randomBytes(16);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 1 });
    const withTrailing = new Uint8Array(signed.signature.length + 1);
    withTrailing.set(signed.signature, 0);
    withTrailing[withTrailing.length - 1] = 0;
    assert.throws(() => parseStrictDerEcdsaSignature(withTrailing), SignatureError);
  });

  it("rejects non-minimal DER length and negative integers", () => {
    // SEQUENCE with a long-form length (0x81 0x08) for an 8-byte payload is non-minimal.
    assert.throws(
      () =>
        parseStrictDerEcdsaSignature(
          new Uint8Array([0x30, 0x81, 0x08, 0x02, 0x03, 0x01, 0x00, 0x01, 0x02, 0x01, 0x01]),
        ),
      SignatureError,
    );
    // INTEGER whose leading content byte has the sign bit set is negative.
    const negative = new Uint8Array([
      0x30, 0x08, 0x02, 0x02, 0xff, 0xff, 0x02, 0x02, 0x00, 0x01,
    ]);
    assert.throws(() => parseStrictDerEcdsaSignature(negative), SignatureError);
  });
});

describe("end-to-end signature verification", () => {
  it("rejects a tampered signature and does not mutate counter state", async () => {
    const key = generateKey("es256");
    const credentialStore = new InMemoryCredentialStore();
    const challengeStore = new InMemoryChallengeStore();
    credentialStore.save(makeRecord({ key, algorithm: "es256", signCount: 10 }));

    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 11 });
    const tampered = new Uint8Array(signed.signature);
    tampered[tampered.length - 1] ^= 0x01;

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, signature: tampered, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "SIGNATURE_INVALID",
    );
    assert.equal(credentialStore.get(CREDENTIAL_ID)?.signCount, 10);
  });

  it("rejects a high-S assertion through the full pipeline", async () => {
    const key = generateKey("es256");
    const credentialStore = new InMemoryCredentialStore();
    const challengeStore = new InMemoryChallengeStore();
    credentialStore.save(makeRecord({ key, algorithm: "es256", signCount: 10 }));

    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 11 });
    signed.signature = ecdsaHighSVariant(signed.signature);

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "SIGNATURE_INVALID",
    );
  });

  it("accepts SPKI DER and KeyObject public key representations", async () => {
    for (const representation of ["spki", "keyObject"] as const) {
      const key = generateKey("es256");
      const credentialStore = new InMemoryCredentialStore();
      const challengeStore = new InMemoryChallengeStore();
      const spki = new Uint8Array(key.publicKey.export({ format: "der", type: "spki" }) as ArrayBuffer);
      credentialStore.save(
        makeRecord({
          key,
          algorithm: "es256",
          publicKey: representation === "spki" ? spki : { keyObject: key.publicKey },
        }),
      );

      const challenge = randomBytes(16);
      challengeStore.issue(challenge);
      const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 11 });
      const result = await verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
      });
      assert.equal(result.signCount, 11);
    }
  });

  it("supports an injected signature verifier", async () => {
    const key = generateKey("ed25519");
    const credentialStore = new InMemoryCredentialStore();
    const challengeStore = new InMemoryChallengeStore();
    credentialStore.save(makeRecord({ key, algorithm: "ed25519", signCount: 5 }));

    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "ed25519", challenge, signCount: 6 });

    let calls = 0;
    const result = await verifyAssertion({
      assertion: { ...signed, credentialId: CREDENTIAL_ID },
      rpId: RP_ID,
      expectedOrigin: ORIGIN,
      challenge: challengeStore,
      credentialStore,
      signatureVerifier: {
        verify: (parameters) => {
          calls += 1;
          assert.equal(parameters.algorithm, -8);
          return true;
        },
      },
    });
    assert.equal(calls, 1);
    assert.equal(result.signCount, 6);
  });

  it("propagates a false result from the injected verifier", async () => {
    const key = generateKey("es256");
    const credentialStore = new InMemoryCredentialStore();
    const challengeStore = new InMemoryChallengeStore();
    credentialStore.save(makeRecord({ key, algorithm: "es256", signCount: 5 }));

    const challenge = randomBytes(16);
    challengeStore.issue(challenge);
    const signed = makeSignedAssertion({ key, algorithm: "es256", challenge, signCount: 6 });

    await assert.rejects(
      verifyAssertion({
        assertion: { ...signed, credentialId: CREDENTIAL_ID },
        rpId: RP_ID,
        expectedOrigin: ORIGIN,
        challenge: challengeStore,
        credentialStore,
        signatureVerifier: { verify: () => false },
      }),
      (error: unknown) => (error as WebAuthnVerificationError).code === "SIGNATURE_INVALID",
    );
  });
});
