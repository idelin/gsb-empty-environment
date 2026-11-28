/**
 * Minimal structural JSON Web Key types. They are assignment-compatible with
 * the DOM/`crypto` JWK shapes but keep this package independent of DOM lib.
 */
export interface JsonWebKey {
  kty?: string;
  use?: string;
  key_ops?: string[];
  alg?: string;
  ext?: boolean;
  crv?: string;
  x?: string;
  y?: string;
  d?: string;
  n?: string;
  e?: string;
  p?: string;
  q?: string;
  dp?: string;
  dq?: string;
  qi?: string;
  oth?: unknown[];
  k?: string;
  [parameter: string]: unknown;
}

export type KeyAlgorithmName = "ES256" | "EdDSA" | "RS256";

/** COSE algorithm identifiers used by WebAuthn. */
export const COSE_ALG = {
  ES256: -7,
  EDDSA: -8,
  RS256: -257,
} as const;

export function algorithmNameFromCose(alg: number): KeyAlgorithmName {
  switch (alg) {
    case COSE_ALG.ES256:
      return "ES256";
    case COSE_ALG.EDDSA:
      return "EdDSA";
    case COSE_ALG.RS256:
      return "RS256";
    default:
      throw new TypeError(`Unsupported COSE algorithm identifier: ${alg}`);
  }
}
