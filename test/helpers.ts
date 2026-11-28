import {
  createHash,
  generateKeyPairSync,
  sign as cryptoSign,
  type KeyObject,
} from "node:crypto";
import { RSA_PKCS1_PADDING } from "node:constants";
import { concatBytes, utf8Encode } from "../src/bytes.ts";
import { encodeClientDataJSON } from "../src/client-data.ts";
import { COSE_ALG, type JsonWebKey } from "../src/webauthn-types.ts";
import type { CredentialRecord, PublicKeyInput } from "../src/credential-store.ts";
import type { BytesInput } from "../src/bytes.ts";

export const RP_ID = "example.com";
export const ORIGIN = "https://example.com";
export const CREDENTIAL_ID = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]);

export interface GeneratedKey {
  privateKey: KeyObject;
  publicKey: KeyObject;
  jwk: JsonWebKey;
}

export function generateKey(algorithm: "es256" | "ed25519" | "rs256"): GeneratedKey {
  if (algorithm === "es256") {
    const { privateKey, publicKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });
    return { privateKey, publicKey, jwk: publicKey.export({ format: "jwk" }) as JsonWebKey };
  }
  if (algorithm === "ed25519") {
    const { privateKey, publicKey } = generateKeyPairSync("ed25519");
    return { privateKey, publicKey, jwk: publicKey.export({ format: "jwk" }) as JsonWebKey };
  }
  const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  return { privateKey, publicKey, jwk: publicKey.export({ format: "jwk" }) as JsonWebKey };
}

export function coseAlgorithm(algorithm: "es256" | "ed25519" | "rs256"): number {
  if (algorithm === "es256") {
    return COSE_ALG.ES256;
  }
  if (algorithm === "ed25519") {
    return COSE_ALG.EDDSA;
  }
  return COSE_ALG.RS256;
}

export interface AuthDataFlags {
  up?: boolean;
  uv?: boolean;
  be?: boolean;
  bs?: boolean;
  at?: boolean;
  ed?: boolean;
}

export function buildAuthenticatorData(parameters: {
  rpId: string;
  signCount: number;
  flags?: AuthDataFlags;
  extensions?: Uint8Array;
}): Uint8Array {
  const flags = parameters.flags ?? {};
  const rpIdHash = new Uint8Array(createHash("sha256").update(utf8Encode(parameters.rpId)).digest());
  let flagByte = 0;
  if (flags.up ?? true) flagByte |= 0x01;
  if (flags.uv ?? true) flagByte |= 0x04;
  if (flags.be ?? false) flagByte |= 0x08;
  if (flags.bs ?? false) flagByte |= 0x10;
  if (flags.at ?? false) flagByte |= 0x40;
  if (flags.ed ?? false) flagByte |= 0x80;

  const counter = new Uint8Array(4);
  new DataView(counter.buffer).setUint32(0, parameters.signCount, false);
  const base = concatBytes(rpIdHash, Uint8Array.of(flagByte), counter);
  if (parameters.extensions !== undefined) {
    return concatBytes(base, parameters.extensions);
  }
  return base;
}

export function buildClientData(parameters: {
  challenge: BytesInput;
  origin?: string;
  crossOrigin?: boolean;
  type?: string;
}): Uint8Array {
  return encodeClientDataJSON({
    type: parameters.type ?? "webauthn.get",
    challenge: parameters.challenge,
    origin: parameters.origin ?? ORIGIN,
    crossOrigin: parameters.crossOrigin,
  });
}

// NIST P-256 order n, used for ECDSA low-S normalization.
const P256_N = BigInt(
  "0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551",
);

interface DerInteger {
  value: Uint8Array;
}

function derLength(length: number): Uint8Array {
  if (length < 0x80) return Uint8Array.of(length);
  if (length <= 0xff) return Uint8Array.of(0x81, length);
  return Uint8Array.of(0x82, (length >> 8) & 0xff, length & 0xff);
}

function derInteger(bytes: Uint8Array): Uint8Array {
  let trimmed = bytes;
  while (trimmed.length > 1 && trimmed[0] === 0) {
    trimmed = trimmed.slice(1);
  }
  const needsPad = trimmed[0] >= 0x80;
  const content = needsPad ? concatBytes(Uint8Array.of(0), trimmed) : trimmed;
  return concatBytes(Uint8Array.of(0x02), derLength(content.length), content);
}

function derSequence(...items: Uint8Array[]): Uint8Array {
  const content = concatBytes(...items);
  return concatBytes(Uint8Array.of(0x30), derLength(content.length), content);
}

export function parseDerIntegers(signature: Uint8Array): { r: bigint; s: bigint; raw: DerInteger[] } {
  // Signature produced by node is guaranteed well-formed here; test-only parser.
  let offset = 1;
  const readLength = (): number => {
    const first = signature[offset];
    offset += 1;
    if ((first & 0x80) === 0) return first;
    const count = first & 0x7f;
    let length = 0;
    for (let index = 0; index < count; index += 1) {
      length = length * 256 + signature[offset + index];
    }
    offset += count;
    return length;
  };
  readLength();
  const integers: DerInteger[] = [];
  for (let which = 0; which < 2; which += 1) {
    offset += 1; // INTEGER tag
    const length = readLength();
    integers.push({ value: signature.slice(offset, offset + length) });
    offset += length;
  }
  const toBigInt = (value: Uint8Array): bigint => {
    let result = 0n;
    for (const byte of value) {
      result = (result << 8n) | BigInt(byte);
    }
    return result;
  };
  return { r: toBigInt(integers[0].value), s: toBigInt(integers[1].value), raw: integers };
}

export function normalizeEcdsaSignatureToLowS(signature: Uint8Array): Uint8Array {
  const { r, s, raw } = parseDerIntegers(signature);
  const halfN = P256_N / 2n;
  let finalS = s;
  if (s > halfN) {
    finalS = P256_N - s;
  }
  const sBytes = bigIntToBytes(finalS, raw[1].value.length);
  return derSequence(derInteger(raw[0].value), derInteger(sBytes));
}

export function ecdsaHighSVariant(signature: Uint8Array): Uint8Array {
  const { r, s, raw } = parseDerIntegers(signature);
  const halfN = P256_N / 2n;
  const highS = s <= halfN ? P256_N - s : s;
  const sBytes = bigIntToBytes(highS, raw[1].value.length);
  return derSequence(derInteger(raw[0].value), derInteger(sBytes));
}

function bigIntToBytes(value: bigint, minLength: number): Uint8Array {
  let hex = value.toString(16);
  if (hex.length % 2 !== 0) hex = `0${hex}`;
  const bytes = new Uint8Array(hex.length / 2);
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
  }
  if (bytes.length < minLength) {
    const padded = new Uint8Array(minLength);
    padded.set(bytes, minLength - bytes.length);
    return padded;
  }
  return bytes;
}

export function signAssertion(parameters: {
  algorithm: "es256" | "ed25519" | "rs256";
  privateKey: KeyObject;
  authenticatorData: Uint8Array;
  clientDataJSON: Uint8Array;
}): Uint8Array {
  const signedData = concatBytes(
    parameters.authenticatorData,
    new Uint8Array(createHash("sha256").update(parameters.clientDataJSON).digest()),
  );
  if (parameters.algorithm === "rs256") {
    return new Uint8Array(
      cryptoSign("sha256", Buffer.from(signedData), {
        key: parameters.privateKey,
        padding: RSA_PKCS1_PADDING,
      }),
    );
  }
  const raw = new Uint8Array(cryptoSign(null, Buffer.from(signedData), parameters.privateKey));
  if (parameters.algorithm === "es256") {
    return normalizeEcdsaSignatureToLowS(raw);
  }
  return raw;
}

export function makeRecord(parameters: {
  key: GeneratedKey;
  algorithm: "es256" | "ed25519" | "rs256";
  signCount?: number;
  backupEligible?: boolean;
  backupState?: boolean;
  publicKey?: PublicKeyInput;
}): CredentialRecord {
  return {
    credentialId: CREDENTIAL_ID,
    publicKey: parameters.publicKey ?? parameters.key.jwk,
    algorithm: coseAlgorithm(parameters.algorithm),
    signCount: parameters.signCount ?? 10,
    backupEligible: parameters.backupEligible ?? false,
    backupState: parameters.backupState ?? false,
    locked: false,
  };
}

export function makeSignedAssertion(parameters: {
  key: GeneratedKey;
  algorithm: "es256" | "ed25519" | "rs256";
  challenge: BytesInput;
  signCount: number;
  flags?: AuthDataFlags;
  rpId?: string;
  origin?: string;
  crossOrigin?: boolean;
  extensions?: Uint8Array;
  type?: string;
}): { authenticatorData: Uint8Array; clientDataJSON: Uint8Array; signature: Uint8Array } {
  const authenticatorData = buildAuthenticatorData({
    rpId: parameters.rpId ?? RP_ID,
    signCount: parameters.signCount,
    flags: parameters.flags,
    extensions: parameters.extensions,
  });
  const clientDataJSON = buildClientData({
    challenge: parameters.challenge,
    origin: parameters.origin,
    crossOrigin: parameters.crossOrigin,
    type: parameters.type,
  });
  const signature = signAssertion({
    algorithm: parameters.algorithm,
    privateKey: parameters.key.privateKey,
    authenticatorData,
    clientDataJSON,
  });
  return { authenticatorData, clientDataJSON, signature };
}
