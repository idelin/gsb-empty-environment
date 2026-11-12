import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { describe, it } from "node:test";
import {
  SdJwtVerificationError,
  verifyKbJwt,
} from "../src/index.ts";
import { buildFixture, buildPresentation, fakeJwt, fakeKbJwt, sha256B64 } from "./helpers.ts";

function expectCode(code: string, fn: () => unknown): void {
  assert.throws(
    fn,
    (error: unknown) =>
      error instanceof SdJwtVerificationError && error.code === code,
    `expected SdJwtVerificationError with code ${code}`,
  );
}

function validSdJwtString(): string {
  const { signedClaims, disclosures } = buildFixture();
  const issuerJwt = fakeJwt(signedClaims);
  return buildPresentation(issuerJwt, [disclosures.ssn]).replace(/~$/, "") + "~";
}

describe("verifyKbJwt", () => {
  it("accepts a KB-JWT whose sd_hash, nonce and aud match", () => {
    const presentedSdJwt = validSdJwtString();
    const kbJwt = fakeKbJwt({
      sd_hash: sha256B64(presentedSdJwt),
      nonce: "nonce-123",
      aud: "https://verifier.example",
      iat: 1_700_000_000,
    });

    const result = verifyKbJwt({
      kbJwt,
      presentedSdJwt,
      expectedNonce: "nonce-123",
      expectedAud: "https://verifier.example",
    });
    assert.equal(result.sdHash, sha256B64(presentedSdJwt));
    assert.equal(result.header.typ, "kb+jwt");
  });

  it("accepts the expected audience when aud is an array", () => {
    const presentedSdJwt = validSdJwtString();
    const kbJwt = fakeKbJwt({
      sd_hash: sha256B64(presentedSdJwt),
      nonce: "n",
      aud: ["https://other.example", "https://verifier.example"],
    });
    assert.doesNotThrow(() =>
      verifyKbJwt({
        kbJwt,
        presentedSdJwt,
        expectedNonce: "n",
        expectedAud: "https://verifier.example",
      }));
  });

  it("rejects an sd_hash computed over a tampered SD-JWT string", () => {
    const presentedSdJwt = validSdJwtString();
    const kbJwt = fakeKbJwt({
      sd_hash: sha256B64(presentedSdJwt + "extra-disclosure~"),
      nonce: "nonce-123",
      aud: "https://verifier.example",
    });
    expectCode("KB_SD_HASH_MISMATCH", () =>
      verifyKbJwt({
        kbJwt,
        presentedSdJwt,
        expectedNonce: "nonce-123",
        expectedAud: "https://verifier.example",
      }));
  });

  it("rejects nonce and aud mismatches independently", () => {
    const presentedSdJwt = validSdJwtString();
    const base = {
      sd_hash: sha256B64(presentedSdJwt),
      nonce: "nonce-123",
      aud: "https://verifier.example",
    };

    expectCode("KB_NONCE_MISMATCH", () =>
      verifyKbJwt({
        kbJwt: fakeKbJwt({ ...base, nonce: "wrong" }),
        presentedSdJwt,
        expectedNonce: "nonce-123",
        expectedAud: "https://verifier.example",
      }));

    expectCode("KB_AUD_MISMATCH", () =>
      verifyKbJwt({
        kbJwt: fakeKbJwt({ ...base, aud: "https://attacker.example" }),
        presentedSdJwt,
        expectedNonce: "nonce-123",
        expectedAud: "https://verifier.example",
      }));
  });

  it("rejects malformed KB-JWTs and wrong typ", () => {
    const presentedSdJwt = validSdJwtString();
    const options = {
      presentedSdJwt,
      expectedNonce: "n",
      expectedAud: "a",
    };

    expectCode("MALFORMED_KB_JWT", () =>
      verifyKbJwt({ ...options, kbJwt: "only.two" }));

    // Header is rebuilt explicitly to guarantee a wrong typ.
    const header = Buffer.from(JSON.stringify({ alg: "ES256", typ: "jwt" }))
      .toString("base64url");
    const payload = Buffer.from(
      JSON.stringify({
        sd_hash: sha256B64(presentedSdJwt),
        nonce: "n",
        aud: "a",
      }),
    ).toString("base64url");
    expectCode("MALFORMED_KB_JWT", () =>
      verifyKbJwt({ ...options, kbJwt: `${header}.${payload}.sig` }));
  });
});
