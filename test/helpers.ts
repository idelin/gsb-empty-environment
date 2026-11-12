import { createHash } from "node:crypto";
import { b64urlEncode } from "../src/base64url.ts";
import { encodeDisclosure } from "../src/disclosures.ts";

export function sha256B64(input: string): string {
  return b64urlEncode(
    new Uint8Array(
      createHash("sha256").update(new TextEncoder().encode(input)).digest(),
    ),
  );
}

export function digestOf(encodedDisclosure: string): string {
  return sha256B64(encodedDisclosure);
}

export function propertyDisclosure(
  salt: string,
  name: string,
  value: unknown,
): string {
  return encodeDisclosure([salt, name, value]);
}

export function arrayItemDisclosure(
  salt: string,
  value: unknown,
): string {
  return encodeDisclosure([salt, value]);
}

export function arrayMarker(digest: string): { "...": string } {
  return { "...": digest };
}

/** Unsigned stand-in for an issuer compact JWT; signature is out of scope. */
export function fakeJwt(claims: Record<string, unknown>): string {
  const header = b64urlEncode(JSON.stringify({ alg: "ES256", typ: "sd-jwt" }));
  const payload = b64urlEncode(JSON.stringify(claims));
  return `${header}.${payload}.signature`;
}

/** Unsigned stand-in for a KB-JWT with attacker-controlled claims. */
export function fakeKbJwt(
  payload: Record<string, unknown>,
  alg = "ES256",
): string {
  const header = b64urlEncode(JSON.stringify({ alg, typ: "kb+jwt" }));
  return `${header}.${b64urlEncode(JSON.stringify(payload))}.kbsig`;
}

export function buildPresentation(
  issuerJwt: string,
  disclosures: readonly string[],
  kbJwt?: string,
): string {
  const body = [issuerJwt, ...disclosures].join("~") + "~";
  return kbJwt ? body + kbJwt : body;
}

/** A realistic signed-claims fixture mixing plaintext and selective claims. */
export function buildFixture() {
  const ssn = propertyDisclosure("salt-ssn", "ssn", "123-45-6789");
  const street = propertyDisclosure(
    "salt-street",
    "street_address",
    "123 Market St",
  );
  const nestedSecret = propertyDisclosure(
    "salt-nested",
    "loyalty_number",
    "L-99",
  );
  const itemA = arrayItemDisclosure("salt-item-a", "redacted-item-1");
  const itemB = arrayItemDisclosure("salt-item-b", "redacted-item-2");

  const signedClaims = {
    iss: "https://issuer.example",
    sub: "user-42",
    given_name: "Ada",
    _sd: [digestOf(ssn)],
    address: {
      locality: "SF",
      _sd: [digestOf(street)],
    },
    tags: ["public-tag", arrayMarker(digestOf(itemA)), arrayMarker(digestOf(itemB))],
    nested: {
      _sd: [digestOf(nestedSecret)],
    },
  };

  return {
    signedClaims,
    disclosures: { ssn, street, nestedSecret, itemA, itemB },
  };
}
