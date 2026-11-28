import { cborItemLength } from "./cbor.ts";
import { AuthenticatorDataError } from "./errors.ts";
import { readUint16Be, readUint32Be, toBytes, type BytesInput } from "./bytes.ts";

export const AUTHENTICATOR_DATA_MIN_LENGTH = 37;

export const FLAG_UP = 0x01;
export const FLAG_UV = 0x04;
export const FLAG_BE = 0x08;
export const FLAG_BS = 0x10;
export const FLAG_AT = 0x40;
export const FLAG_ED = 0x80;

const RESERVED_FLAG_MASK = 0x22;

export interface ParsedAuthenticatorData {
  readonly raw: Uint8Array;
  readonly rpIdHash: Uint8Array;
  readonly flags: number;
  readonly up: boolean;
  readonly uv: boolean;
  readonly be: boolean;
  readonly bs: boolean;
  readonly at: boolean;
  readonly ed: boolean;
  readonly signCount: number;
  readonly attestedCredentialData: Uint8Array | null;
  readonly extensions: Uint8Array | null;
}

/**
 * Parse the raw authenticator data structure defined by WebAuthn:
 * rpIdHash (32) || flags (1) || signCount (4) [ || attestedCredentialData ]
 * [ || extensions ].
 */
export function parseAuthenticatorData(input: BytesInput): ParsedAuthenticatorData {
  const raw = toBytes(input);
  if (raw.length < AUTHENTICATOR_DATA_MIN_LENGTH) {
    throw new AuthenticatorDataError(
      `authenticatorData must be at least ${AUTHENTICATOR_DATA_MIN_LENGTH} bytes, got ${raw.length}`,
    );
  }

  const rpIdHash = raw.slice(0, 32);
  const flags = raw[32];
  const signCount = readUint32Be(raw, 33);

  if ((flags & RESERVED_FLAG_MASK) !== 0) {
    throw new AuthenticatorDataError("Reserved authenticator data flag bits must be zero");
  }

  const up = (flags & FLAG_UP) === FLAG_UP;
  const uv = (flags & FLAG_UV) === FLAG_UV;
  const be = (flags & FLAG_BE) === FLAG_BE;
  const bs = (flags & FLAG_BS) === FLAG_BS;
  const at = (flags & FLAG_AT) === FLAG_AT;
  const ed = (flags & FLAG_ED) === FLAG_ED;

  let cursor = AUTHENTICATOR_DATA_MIN_LENGTH;
  let attestedCredentialData: Uint8Array | null = null;

  if (at) {
    if (cursor + 18 > raw.length) {
      throw new AuthenticatorDataError("AT flag set but attested credential data is truncated");
    }
    const credentialIdLengthOffset = cursor + 16;
    const credentialIdLength = readUint16Be(raw, credentialIdLengthOffset);
    const credentialIdStart = credentialIdLengthOffset + 2;
    const credentialIdEnd = credentialIdStart + credentialIdLength;
    if (credentialIdEnd > raw.length) {
      throw new AuthenticatorDataError("Attested credential identifier exceeds authenticator data");
    }
    const credentialPublicKeyStart = credentialIdEnd;
    const publicKeyLength = cborItemLength(raw, credentialPublicKeyStart);
    const credentialPublicKeyEnd = credentialPublicKeyStart + publicKeyLength;
    if (credentialPublicKeyEnd > raw.length) {
      throw new AuthenticatorDataError("Attested credential public key exceeds authenticator data");
    }
    attestedCredentialData = raw.slice(cursor, credentialPublicKeyEnd);
    cursor = credentialPublicKeyEnd;
  }

  let extensions: Uint8Array | null = null;
  if (ed) {
    if (cursor >= raw.length) {
      throw new AuthenticatorDataError("ED flag set but extension data is missing");
    }
    const extensionLength = cborItemLength(raw, cursor);
    const extensionEnd = cursor + extensionLength;
    if (extensionEnd !== raw.length) {
      throw new AuthenticatorDataError("Trailing bytes after CBOR extension data");
    }
    extensions = raw.slice(cursor, extensionEnd);
    cursor = extensionEnd;
  }

  if (cursor !== raw.length) {
    throw new AuthenticatorDataError("Trailing bytes after parsed authenticator data");
  }

  return {
    raw,
    rpIdHash,
    flags,
    up,
    uv,
    be,
    bs,
    at,
    ed,
    signCount,
    attestedCredentialData,
    extensions,
  };
}
