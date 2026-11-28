import {
  createHash,
  createPublicKey,
  KeyObject,
  verify as cryptoVerify,
} from "node:crypto";
import { RSA_PKCS1_PADDING } from "node:constants";
import { toBytes, type BytesInput } from "./bytes.ts";
import { PublicKeyUnsupportedError, SignatureError } from "./errors.ts";
import { COSE_ALG, algorithmNameFromCose, type JsonWebKey } from "./webauthn-types.ts";
import type { PublicKeyInput } from "./credential-store.ts";

export interface SignatureVerifier {
  verify(parameters: {
    algorithm: number;
    publicKey: PublicKeyInput;
    signedData: Uint8Array;
    signature: Uint8Array;
  }): boolean | Promise<boolean>;
}

export interface EcdsaSignature {
  r: Uint8Array;
  s: Uint8Array;
}

// NIST P-256 group order n (SEC2 / FIPS 186-4).
const P256_N = new Uint8Array([
  0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
  0xbc, 0xe6, 0xfa, 0xad, 0xa7, 0x17, 0x9e, 0x84, 0xf3, 0xb9, 0xca, 0xc2, 0xfc, 0x63, 0x25, 0x51,
]);

const HALF_P256_N = new Uint8Array([
  0x7f, 0xff, 0xff, 0xff, 0x80, 0x00, 0x00, 0x00, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
  0x5e, 0x73, 0x7d, 0x56, 0xd3, 0x8b, 0xcf, 0x42, 0x79, 0xdc, 0xe5, 0x61, 0x7e, 0x31, 0x92, 0xa8,
]);

export function sha256(input: Uint8Array): Uint8Array {
  return new Uint8Array(createHash("sha256").update(input).digest());
}

/**
 * Parse an ECDSA-Sig-Value DER sequence (RFC 3279) with strict rules:
 * minimal length encodings, positive non-zero integers and no trailing bytes.
 */
export function parseStrictDerEcdsaSignature(input: BytesInput): EcdsaSignature {
  const bytes = toBytes(input);
  const cursor = { value: 0 };

  if (bytes.length < 2 || bytes[cursor.value] !== 0x30) {
    throw new SignatureError("Invalid ECDSA DER encoding: expected SEQUENCE tag");
  }
  cursor.value += 1;
  const sequenceEnd = readDerLength(bytes, cursor);
  if (sequenceEnd !== bytes.length) {
    throw new SignatureError("Invalid ECDSA DER encoding: trailing bytes after SEQUENCE");
  }

  const r = readDerInteger(bytes, cursor);
  const s = readDerInteger(bytes, cursor);
  if (cursor.value !== sequenceEnd) {
    throw new SignatureError("Invalid ECDSA DER encoding: unexpected length after integers");
  }
  return { r, s };
}

/** Read a DER length at `cursor` and return the offset just past the content. */
function readDerLength(bytes: Uint8Array, cursor: { value: number }): number {
  if (cursor.value >= bytes.length) {
    throw new SignatureError("Invalid ECDSA DER encoding: truncated length");
  }
  const first = bytes[cursor.value];
  cursor.value += 1;
  if ((first & 0x80) === 0) {
    const end = cursor.value + first;
    if (end > bytes.length) {
      throw new SignatureError("Invalid ECDSA DER encoding: content exceeds input");
    }
    return end;
  }
  const lengthBytes = first & 0x7f;
  if (lengthBytes === 0) {
    throw new SignatureError("Invalid ECDSA DER encoding: indefinite length is forbidden");
  }
  if (lengthBytes > 4 || cursor.value + lengthBytes > bytes.length) {
    throw new SignatureError("Invalid ECDSA DER encoding: malformed long length");
  }
  let length = 0;
  for (let index = 0; index < lengthBytes; index += 1) {
    length = length * 256 + bytes[cursor.value + index];
  }
  if (length < 0x80) {
    throw new SignatureError("Invalid ECDSA DER encoding: non-minimal long length");
  }
  const contentStart = cursor.value + lengthBytes;
  const end = contentStart + length;
  if (end > bytes.length) {
    throw new SignatureError("Invalid ECDSA DER encoding: content exceeds input");
  }
  cursor.value = contentStart;
  return end;
}

function readDerInteger(bytes: Uint8Array, cursor: { value: number }): Uint8Array {
  if (cursor.value >= bytes.length || bytes[cursor.value] !== 0x02) {
    throw new SignatureError("Invalid ECDSA DER encoding: expected INTEGER tag");
  }
  cursor.value += 1;
  const contentEnd = readDerLength(bytes, cursor);
  const contentStart = cursor.value;
  const integerLength = contentEnd - contentStart;
  if (integerLength === 0) {
    throw new SignatureError("Invalid ECDSA DER encoding: empty INTEGER");
  }
  const first = bytes[contentStart];
  if (first === 0x00) {
    if (integerLength === 1 || bytes[contentStart + 1] < 0x80) {
      throw new SignatureError("Invalid ECDSA DER encoding: non-minimal INTEGER");
    }
  } else if (first >= 0x80) {
    throw new SignatureError("Invalid ECDSA DER encoding: negative INTEGER");
  }
  const value = bytes.slice(contentStart, contentEnd);
  cursor.value = contentEnd;
  return value;
}

function compareUnsignedBytes(a: Uint8Array, b: Uint8Array): number {
  if (a.length !== b.length) {
    return a.length > b.length ? 1 : -1;
  }
  for (let index = 0; index < a.length; index += 1) {
    if (a[index] !== b[index]) {
      return a[index] > b[index] ? 1 : -1;
    }
  }
  return 0;
}

/** Enforce BIP/SEC style low-S normalization for P-256 ECDSA signatures. */
export function assertLowSP256(signature: EcdsaSignature): void {
  const s = trimLeadingZeros(signature.s);
  if (s.length === 0 || compareUnsignedBytes(s, P256_N) >= 0) {
    throw new SignatureError("ECDSA scalar s is out of range");
  }
  if (compareUnsignedBytes(s, HALF_P256_N) > 0) {
    throw new SignatureError("ECDSA signature uses high-S and is not in canonical form");
  }
}

function trimLeadingZeros(bytes: Uint8Array): Uint8Array {
  let offset = 0;
  while (offset < bytes.length - 1 && bytes[offset] === 0) {
    offset += 1;
  }
  return bytes.slice(offset);
}

export function loadPublicKey(publicKey: PublicKeyInput): KeyObject {
  if (publicKey instanceof KeyObject) {
    if (publicKey.type !== "public") {
      throw new PublicKeyUnsupportedError("KeyObject must be a public key");
    }
    return publicKey;
  }
  if (publicKey instanceof Uint8Array) {
    try {
      return createPublicKey({ key: Buffer.from(publicKey), format: "der", type: "spki" });
    } catch (cause) {
      throw new PublicKeyUnsupportedError("Failed to import SPKI DER public key", { cause });
    }
  }
  if (typeof publicKey === "object" && publicKey !== null && "keyObject" in publicKey) {
    const keyObject = (publicKey as { keyObject: unknown }).keyObject;
    if (!(keyObject instanceof KeyObject) || keyObject.type !== "public") {
      throw new PublicKeyUnsupportedError("keyObject wrapper must contain a public KeyObject");
    }
    return keyObject;
  }
  if (typeof publicKey === "object" && publicKey !== null && "kty" in publicKey) {
    try {
      return createPublicKey({ key: publicKey as JsonWebKey, format: "jwk" });
    } catch (cause) {
      throw new PublicKeyUnsupportedError("Failed to import JWK public key", { cause });
    }
  }
  throw new PublicKeyUnsupportedError("Unsupported public key representation");
}

export function assertKeyMatchesAlgorithm(keyObject: KeyObject, algorithm: number): void {
  let jwk: JsonWebKey | undefined;
  try {
    jwk = keyObject.export({ format: "jwk" }) as JsonWebKey;
  } catch {
    jwk = undefined;
  }
  const asymmetricKeyType = keyObject.asymmetricKeyType;

  if (algorithm === COSE_ALG.ES256) {
    if (asymmetricKeyType !== "ec" || (jwk !== undefined && jwk.crv !== undefined && jwk.crv !== "P-256")) {
      throw new PublicKeyUnsupportedError("ES256 requires an EC P-256 public key");
    }
    return;
  }
  if (algorithm === COSE_ALG.EDDSA) {
    if (
      asymmetricKeyType !== "ed25519" ||
      (jwk !== undefined && jwk.crv !== undefined && jwk.crv !== "Ed25519")
    ) {
      throw new PublicKeyUnsupportedError("EdDSA requires an Ed25519 public key");
    }
    return;
  }
  if (algorithm === COSE_ALG.RS256) {
    if (asymmetricKeyType !== "rsa" || (jwk !== undefined && jwk.kty !== undefined && jwk.kty !== "RSA")) {
      throw new PublicKeyUnsupportedError("RS256 requires an RSA public key");
    }
    return;
  }
  throw new PublicKeyUnsupportedError(`Unsupported COSE algorithm ${algorithm}`);
}

function verifyWithNodeCrypto(
  algorithm: number,
  keyObject: KeyObject,
  signedData: Uint8Array,
  signature: Uint8Array,
): boolean {
  const name = algorithmNameFromCose(algorithm);
  try {
    if (name === "ES256") {
      // Structural/canonical checks happen before crypto verification.
      return cryptoVerify(null, Buffer.from(signedData), keyObject, Buffer.from(signature)) as boolean;
    }
    if (name === "EdDSA") {
      return cryptoVerify(null, Buffer.from(signedData), keyObject, Buffer.from(signature)) as boolean;
    }
    return cryptoVerify(
      "sha256",
      Buffer.from(signedData),
      { key: keyObject, padding: RSA_PKCS1_PADDING },
      Buffer.from(signature),
    ) as boolean;
  } catch (cause) {
    throw new SignatureError("Signature verification failed", { cause });
  }
}

export interface VerifySignatureParameters {
  algorithm: number;
  publicKey: PublicKeyInput;
  signedData: Uint8Array;
  signature: Uint8Array;
  verifier?: SignatureVerifier;
  /** Disable low-S enforcement only for explicitly downgraded hosts. */
  requireLowS?: boolean;
}

export function verifyAssertionSignature(parameters: VerifySignatureParameters): boolean {
  const { algorithm, publicKey, signedData, signature, verifier, requireLowS = true } = parameters;
  const keyObject = loadPublicKey(publicKey);
  assertKeyMatchesAlgorithm(keyObject, algorithm);

  if (algorithm === COSE_ALG.ES256) {
    const parsed = parseStrictDerEcdsaSignature(signature);
    if (requireLowS) {
      assertLowSP256(parsed);
    }
  }

  if (verifier !== undefined) {
    return verifier.verify({ algorithm, publicKey, signedData, signature }) === true;
  }
  return verifyWithNodeCrypto(algorithm, keyObject, signedData, signature);
}
