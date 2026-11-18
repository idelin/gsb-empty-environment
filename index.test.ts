import assert from "node:assert/strict";
import { createHash, createHmac } from "node:crypto";
import test from "node:test";

import {
  SdJwtValidationError,
  validateKeyBindingJwt,
  verifySdJwt,
  verifySdJwtDisclosures,
} from "./index.ts";

function base64urlJson(value: unknown): string {
  return Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
}

function makeDisclosure(salt: string, name: string, value: unknown): string {
  return base64urlJson([salt, name, value]);
}

function digest(disclosure: string, alg = "sha-256"): string {
  return createHash(alg.replace("-", "")).update(disclosure, "utf8").digest("base64url");
}

function makeHmacJwt(
  payload: Record<string, unknown>,
  key = "issuer-secret",
  header: Record<string, unknown> = {},
): string {
  const encodedHeader = base64urlJson({ alg: "HS256", typ: "JWT", ...header });
  const encodedPayload = base64urlJson(payload);
  const signingInput = `${encodedHeader}.${encodedPayload}`;
  const signature = createHmac("sha256", key).update(signingInput).digest("base64url");
  return `${signingInput}.${signature}`;
}

function makeSdJwt(
  payload: Record<string, unknown>,
  disclosures: readonly string[],
  issuerKey = "issuer-secret",
): { issuerJwt: string; sdJwt: string } {
  const issuerJwt = makeHmacJwt(payload, issuerKey);
  const sdJwt =
    disclosures.length === 0
      ? `${issuerJwt}~`
      : `${issuerJwt}~${disclosures.join("~")}~`;
  return { issuerJwt, sdJwt };
}

function errorCode(fn: () => unknown): string {
  try {
    fn();
  } catch (error) {
    assert.ok(error instanceof SdJwtValidationError);
    return error.code;
  }
  assert.fail("Expected verification to throw");
}

test("accepts top-level and nested disclosures regardless of presentation order", () => {
  const country = makeDisclosure("country-salt", "country", "DE");
  const address = makeDisclosure("address-salt", "address", {
    street: "Main Street",
    _sd: [digest(country)],
  });
  const payload = {
    _sd_alg: "sha-256",
    name: "Alice",
    _sd: [digest(address)],
  };

  const result = verifySdJwtDisclosures(payload, [country, address]);

  assert.deepEqual(result.claims, {
    _sd_alg: "sha-256",
    name: "Alice",
    address: {
      street: "Main Street",
      country: "DE",
    },
  });
  assert.equal(result.disclosures.length, 2);
});

test("rejects a substituted disclosure even when it reuses the original salt", () => {
  const original = makeDisclosure("role-salt", "role", "admin");
  const substituted = makeDisclosure("role-salt", "role", "user");
  const payload = { _sd: [digest(original)] };

  assert.equal(
    errorCode(() => verifySdJwtDisclosures(payload, [substituted])),
    "DISCLOSURE_NOT_SIGNED_OR_PARENT_WITHHELD",
  );
});

test("rejects a nested disclosure when its parent is withheld", () => {
  const child = makeDisclosure("child-salt", "city", "Berlin");
  const parent = makeDisclosure("parent-salt", "address", {
    _sd: [digest(child)],
  });
  const payload = { _sd: [digest(parent)] };

  assert.equal(
    errorCode(() => verifySdJwtDisclosures(payload, [child])),
    "DISCLOSURE_NOT_SIGNED_OR_PARENT_WITHHELD",
  );
});

test("rejects duplicate disclosure salts", () => {
  const first = makeDisclosure("same-salt", "first", 1);
  const second = makeDisclosure("same-salt", "second", 2);
  const payload = { _sd: [digest(first), digest(second)] };

  assert.equal(
    errorCode(() => verifySdJwtDisclosures(payload, [first, second])),
    "DUPLICATE_SALT",
  );
});

test("rejects a disclosure hash that occurs more than once", () => {
  const disclosure = makeDisclosure("role-salt", "role", "admin");
  const disclosureHash = digest(disclosure);
  const payload = { _sd: [disclosureHash, disclosureHash] };

  assert.equal(
    errorCode(() => verifySdJwtDisclosures(payload, [disclosure])),
    "DUPLICATE_DIGEST",
  );
});

test("rejects disclosures hashed with an algorithm that mixes with _sd_alg", () => {
  const disclosure = makeDisclosure("alg-salt", "role", "admin");
  const payload = {
    _sd_alg: "sha-256",
    _sd: [digest(disclosure, "sha-512")],
  };

  assert.equal(
    errorCode(() => verifySdJwtDisclosures(payload, [disclosure])),
    "ALGORITHM_MIXING",
  );
});

test("uses _sd_alg for the complete disclosure tree", () => {
  const disclosure = makeDisclosure("role-salt", "role", "admin");
  const payload = {
    _sd_alg: "sha-384",
    _sd: [digest(disclosure, "sha-384")],
  };

  const result = verifySdJwtDisclosures(payload, [disclosure]);
  assert.equal(result.claims.role, "admin");
});

test("rejects expired KB-JWTs", () => {
  const disclosure = makeDisclosure("role-salt", "role", "admin");
  const { sdJwt } = makeSdJwt({ _sd: [digest(disclosure)] }, [disclosure]);
  const kbJwt = makeHmacJwt(
    {
      sd_hash: digest(sdJwt),
      nonce: "nonce-1",
      aud: "verifier",
      exp: 1_000,
    },
    "holder-secret",
  );

  assert.equal(
    errorCode(() =>
      validateKeyBindingJwt(kbJwt, sdJwt, {
        expectedNonce: "nonce-1",
        expectedAudience: "verifier",
        now: 1_001,
        holderKey: "holder-secret",
      }),
    ),
    "KB_EXPIRED",
  );
});

test("rejects the whole presentation when any disclosure fails", () => {
  const valid = makeDisclosure("valid-salt", "role", "admin");
  const forged = makeDisclosure("forged-salt", "email", "attacker@example.com");
  const payload = {
    subject: "alice",
    _sd: [digest(valid)],
  };
  const before = JSON.stringify(payload);

  assert.equal(
    errorCode(() => verifySdJwtDisclosures(payload, [valid, forged])),
    "DISCLOSURE_NOT_SIGNED_OR_PARENT_WITHHELD",
  );
  assert.equal(JSON.stringify(payload), before);
  assert.equal("role" in payload, false);
});

test("validates sd_hash, nonce, audience, expiry, and KB-JWT signature together", () => {
  const disclosure = makeDisclosure("role-salt", "role", "admin");
  const { sdJwt } = makeSdJwt({ _sd: [digest(disclosure)] }, [disclosure]);
  const kbJwt = makeHmacJwt(
    {
      sd_hash: digest(sdJwt),
      nonce: "nonce-1",
      aud: ["verifier", "other"],
      exp: 2_000,
    },
    "holder-secret",
  );

  const result = verifySdJwt(
    `${sdJwt}${kbJwt}`,
    {
      expectedNonce: "nonce-1",
      expectedAudience: "verifier",
      now: 1_000,
      holderKey: "holder-secret",
    },
  );

  assert.equal(result.claims.role, "admin");
  assert.equal(result.kb?.signatureVerified, true);
});

test("rejects a KB-JWT whose sd_hash covers different disclosures", () => {
  const disclosure = makeDisclosure("role-salt", "role", "admin");
  const { sdJwt } = makeSdJwt({ _sd: [digest(disclosure)] }, [disclosure]);
  const kbJwt = makeHmacJwt(
    {
      sd_hash: digest(`${sdJwt}forged`),
      nonce: "nonce-1",
      aud: "verifier",
      exp: 2_000,
    },
    "holder-secret",
  );

  assert.equal(
    errorCode(() =>
      validateKeyBindingJwt(kbJwt, sdJwt, {
        expectedNonce: "nonce-1",
        expectedAudience: "verifier",
        now: 1_000,
        holderKey: "holder-secret",
      }),
    ),
    "KB_SD_HASH_MISMATCH",
  );
});

test("rejects missing salts, reserved names, and plaintext name collisions", () => {
  const missingSalt = base64urlJson(["", "role", "admin"]);
  assert.equal(
    errorCode(() => verifySdJwtDisclosures({ _sd: [digest(missingSalt)] }, [missingSalt])),
    "MISSING_SALT",
  );

  const reserved = makeDisclosure("reserved-salt", "_sd", "value");
  assert.equal(
    errorCode(() => verifySdJwtDisclosures({ _sd: [digest(reserved)] }, [reserved])),
    "FORBIDDEN_DISCLOSURE_NAME",
  );

  const collision = makeDisclosure("collision-salt", "role", "admin");
  assert.equal(
    errorCode(() =>
      verifySdJwtDisclosures({ role: "user", _sd: [digest(collision)] }, [collision]),
    ),
    "DISCLOSURE_NAME_CONFLICT",
  );
});
