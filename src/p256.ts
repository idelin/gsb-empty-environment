import { decodeBase64UrlStrict } from "./base64url.ts";
import { JweSecurityError } from "./errors.ts";
import {
  isStrictJsonObject,
  type StrictJsonObject,
  type StrictJsonValue,
} from "./json.ts";

export const P256_P =
  0xffffffff00000001000000000000000000000000ffffffffffffffffffffffffn;
export const P256_A = P256_P - 3n;
export const P256_B =
  0x5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604bn;
export const P256_N =
  0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551n;

export interface P256AffinePoint {
  readonly x: bigint;
  readonly y: bigint;
}

export interface ValidatedEcdhEpk {
  readonly jwk: StrictJsonObject;
  readonly point: P256AffinePoint;
  readonly xBytes: Buffer;
  readonly yBytes: Buffer;
}

function mod(value: bigint, modulus: bigint = P256_P): bigint {
  const result = value % modulus;
  return result < 0n ? result + modulus : result;
}

/**
 * Explicitly checks a P-256 affine point.
 *
 * P-256's group order is the prime n and its cofactor is 1. Therefore every
 * finite point satisfying this equation has order n; rejecting (0, 0) as the
 * infinity encoding rules out the only small-subgroup case without paying for
 * a full scalar multiplication in the request path.
 */
export function validateP256Point(point: P256AffinePoint): P256AffinePoint {
  if (typeof point?.x !== "bigint" || typeof point?.y !== "bigint") {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_INVALID",
      "P-256 coordinates must be integers",
    );
  }

  if (point.x === 0n && point.y === 0n) {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_INFINITY",
      "the point at infinity is not a valid ECDH public key",
    );
  }

  if (
    point.x < 0n ||
    point.y < 0n ||
    point.x >= P256_P ||
    point.y >= P256_P
  ) {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_RANGE",
      "P-256 coordinate is outside the prime field",
      {
        xInRange: point.x >= 0n && point.x < P256_P,
        yInRange: point.y >= 0n && point.y < P256_P,
      },
    );
  }

  const left = mod(point.y * point.y);
  const right = mod(point.x * point.x * point.x + P256_A * point.x + P256_B);
  if (left !== right) {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_CURVE",
      "epk is not on the P-256 curve",
    );
  }

  return point;
}

function coordinateFromJwk(
  jwk: StrictJsonObject,
  name: "x" | "y",
): Buffer {
  const value = jwk[name];
  if (typeof value !== "string") {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_COORDINATE",
      `epk.${name} must be a Base64URL string`,
    );
  }

  const bytes = decodeBase64UrlStrict(value, {
    name: `epk.${name}`,
    maxBytes: 32,
    nonCanonicalCode: "JWE_ECDH_EPK_COORDINATE",
    invalidCode: "JWE_ECDH_EPK_COORDINATE",
  });

  if (bytes.length !== 32) {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_COORDINATE",
      `epk.${name} must encode exactly 32 octets for P-256`,
      { actualBytes: bytes.length },
    );
  }
  return bytes;
}

export function validateEcdhEpk(epk: unknown): ValidatedEcdhEpk {
  if (!isStrictJsonObject(epk as StrictJsonValue)) {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_INVALID",
      "epk must be a JSON object",
    );
  }

  const jwk = epk as StrictJsonObject;
  if (jwk.kty !== "EC") {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_KTY",
      'epk.kty must be exactly "EC"',
    );
  }
  if (jwk.crv !== "P-256") {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_CRV",
      'epk.crv must be exactly "P-256"',
    );
  }
  if (Object.prototype.hasOwnProperty.call(jwk, "d")) {
    throw new JweSecurityError(
      "JWE_ECDH_EPK_PRIVATE_MATERIAL",
      "epk must not contain private key material",
    );
  }

  const xBytes = coordinateFromJwk(jwk, "x");
  const yBytes = coordinateFromJwk(jwk, "y");
  const point = {
    x: BigInt(`0x${xBytes.toString("hex")}`),
    y: BigInt(`0x${yBytes.toString("hex")}`),
  };

  validateP256Point(point);
  return { jwk, point, xBytes, yBytes };
}

export const P256 = {
  p: P256_P,
  a: P256_A,
  b: P256_B,
  n: P256_N,
} as const;
