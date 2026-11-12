import { TrustChainError } from './errors.ts';
import type {
  CompactJwt,
  FederationClaims,
  Jwks,
  JwsHeader,
} from './types.ts';

function base64UrlDecode(segment: string): Uint8Array {
  try {
    const padded = segment.replace(/-/g, '+').replace(/_/g, '/');
    const padding = padded.length % 4 === 0 ? '' : '='.repeat(4 - (padded.length % 4));
    const binary = atob(padded + padding);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  } catch {
    throw new TrustChainError('MALFORMED_JWT', 'token is not valid base64url');
  }
}

function decodeJson<T>(segment: string, part: string): T {
  const bytes = base64UrlDecode(segment);
  const text = new TextDecoder().decode(bytes);
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new TrustChainError('MALFORMED_JWT', `${part} is not valid JSON`);
  }
}

/** Parse a compact JWT into its three parts without verifying anything. */
export function parseCompactJwt(jwt: CompactJwt): {
  header: JwsHeader;
  payload: FederationClaims;
  signingInput: string;
  signature: Uint8Array;
} {
  if (typeof jwt !== 'string' || jwt.length === 0) {
    throw new TrustChainError('MALFORMED_JWT', 'token must be a non-empty string');
  }
  const parts = jwt.split('.');
  if (parts.length !== 3 || parts.some((part) => part.length === 0)) {
    throw new TrustChainError(
      'MALFORMED_JWT',
      'token must have three non-empty dot-separated parts',
    );
  }
  const header = decodeJson<JwsHeader>(parts[0], 'JWT header');
  if (typeof header.alg !== 'string' || header.alg.length === 0) {
    throw new TrustChainError('MALFORMED_JWT', 'JWT header is missing alg');
  }
  const payload = decodeJson<FederationClaims>(parts[1], 'JWT payload');
  if (typeof payload.iss !== 'string' || typeof payload.sub !== 'string') {
    throw new TrustChainError(
      'MALFORMED_JWT',
      'JWT payload must contain string iss and sub',
    );
  }
  if (typeof payload.exp !== 'number') {
    throw new TrustChainError('MALFORMED_JWT', 'JWT payload is missing numeric exp');
  }
  return {
    header,
    payload,
    signingInput: `${parts[0]}.${parts[1]}`,
    signature: base64UrlDecode(parts[2]),
  };
}

/** True when a statement is a self-signed Entity Configuration. */
export function isEntityConfiguration(payload: FederationClaims): boolean {
  return payload.iss === payload.sub;
}

/** Extract and validate the `jwks` claim of an Entity Configuration. */
export function requireJwks(payload: FederationClaims): Jwks {
  if (
    payload.jwks === undefined ||
    typeof payload.jwks !== 'object' ||
    !Array.isArray(payload.jwks.keys) ||
    payload.jwks.keys.length === 0
  ) {
    throw new TrustChainError(
      'MISSING_JWKS',
      `entity configuration for ${payload.sub} has no usable jwks.keys`,
    );
  }
  return payload.jwks;
}
