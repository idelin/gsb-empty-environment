import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  SdJwtVerificationError,
  verifySdJwtPresentation,
} from "../src/index.ts";
import {
  buildFixture,
  buildPresentation,
  digestOf,
  fakeJwt,
  fakeKbJwt,
  propertyDisclosure,
  sha256B64,
} from "./helpers.ts";

function expectCode(code: string, fn: () => unknown): void {
  assert.throws(
    fn,
    (error: unknown) =>
      error instanceof SdJwtVerificationError && error.code === code,
    `expected SdJwtVerificationError with code ${code}`,
  );
}

const NONCE = "challenge-nonce";
const AUD = "https://verifier.example";

function setup() {
  const fixture = buildFixture();
  const issuerJwt = fakeJwt(fixture.signedClaims);
  return { ...fixture, issuerJwt };
}

describe("verifySdJwtPresentation - end to end", () => {
  it("verifies disclosures and holder binding for a full presentation", () => {
    const { signedClaims, disclosures, issuerJwt } = setup();
    const presented = [disclosures.ssn, disclosures.street];
    const sdJwtString = [issuerJwt, ...presented].join("~") + "~";
    const kbJwt = fakeKbJwt({
      sd_hash: sha256B64(sdJwtString),
      nonce: NONCE,
      aud: AUD,
    });

    const result = verifySdJwtPresentation({
      presentation: buildPresentation(issuerJwt, presented, kbJwt),
      signedClaims,
      kb: { expectedNonce: NONCE, expectedAud: AUD },
    });

    assert.equal(result.claims.ssn, "123-45-6789");
    assert.equal(result.claims.address.street_address, "123 Market St");
    assert.equal(result.presentedSdJwt, sdJwtString);
    assert.equal(result.kbJwt, kbJwt);
  });

  it("works without a KB-JWT when holder binding is not required", () => {
    const { signedClaims, disclosures, issuerJwt } = setup();
    const result = verifySdJwtPresentation({
      presentation: buildPresentation(issuerJwt, [disclosures.ssn]),
      signedClaims,
    });
    assert.equal(result.claims.ssn, "123-45-6789");
    assert.equal(result.kbJwt, undefined);
  });

  it("attack: rejects a presentation carrying a substituted issuer-unsigned disclosure", () => {
    const { signedClaims, issuerJwt } = setup();
    const forged = propertyDisclosure("salt-ssn", "ssn", "attacker-ssn");
    expectCode("UNREFERENCED_DISCLOSURE", () =>
      verifySdJwtPresentation({
        presentation: buildPresentation(issuerJwt, [forged]),
        signedClaims,
      }));
  });

  it("attack: rejects duplicate digests in the signed payload", () => {
    const disclosure = propertyDisclosure("salt", "claim", "v");
    const signedClaims = {
      _sd: [digestOf(disclosure)],
      child: { _sd: [digestOf(disclosure)] },
    };
    const issuerJwt = fakeJwt(signedClaims);
    expectCode("DUPLICATE_SD_DIGEST", () =>
      verifySdJwtPresentation({
        presentation: buildPresentation(issuerJwt, [disclosure]),
        signedClaims,
      }));
  });

  it("attack: rejects a disclosed name colliding with a plaintext claim", () => {
    const disclosure = propertyDisclosure("salt", "given_name", "Mallory");
    const signedClaims = {
      given_name: "Ada",
      _sd: [digestOf(disclosure)],
    };
    const issuerJwt = fakeJwt(signedClaims);
    expectCode("CLAIM_NAME_CONFLICT", () =>
      verifySdJwtPresentation({
        presentation: buildPresentation(issuerJwt, [disclosure]),
        signedClaims,
      }));
  });

  it("attack: rejects a KB-JWT whose sd_hash ignores an added disclosure", () => {
    const { signedClaims, disclosures, issuerJwt } = setup();
    const honestString = [issuerJwt, disclosures.ssn].join("~") + "~";
    const kbJwt = fakeKbJwt({
      sd_hash: sha256B64(honestString),
      nonce: NONCE,
      aud: AUD,
    });
    // Holder appends another disclosure but keeps the old sd_hash.
    const presentation = buildPresentation(
      issuerJwt,
      [disclosures.ssn, disclosures.street],
      kbJwt,
    );
    expectCode("KB_SD_HASH_MISMATCH", () =>
      verifySdJwtPresentation({
        presentation,
        signedClaims,
        kb: { expectedNonce: NONCE, expectedAud: AUD },
      }));
  });

  it("requires a KB-JWT when holder binding is configured", () => {
    const { signedClaims, disclosures, issuerJwt } = setup();
    expectCode("MALFORMED_KB_JWT", () =>
      verifySdJwtPresentation({
        presentation: buildPresentation(issuerJwt, [disclosures.ssn]),
        signedClaims,
        kb: { expectedNonce: NONCE, expectedAud: AUD },
      }));
  });

  it("rejects malformed presentations and unsupported hash algorithms", () => {
    const { signedClaims } = setup();
    expectCode("MALFORMED_PRESENTATION", () =>
      verifySdJwtPresentation({ presentation: "not-a-jwt~", signedClaims }));

    expectCode("UNSUPPORTED_SD_ALG", () =>
      verifySdJwtPresentation({
        presentation: buildPresentation(fakeJwt({}), []),
        signedClaims: { _sd_alg: "sha-512" },
      }));
  });
});
