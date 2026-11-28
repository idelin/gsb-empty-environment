import { ClientDataError } from "./errors.ts";
import { base64Encode, base64UrlEncode, toBytes, utf8Decode, type BytesInput } from "./bytes.ts";

export const WEB_AUTHN_GET_TYPE = "webauthn.get";

export interface ParsedClientData {
  readonly raw: Uint8Array;
  readonly type: string;
  readonly challenge: string;
  readonly origin: string;
  readonly crossOrigin: boolean;
  readonly tokenBinding: unknown;
}

/**
 * Parse and minimally validate `clientDataJSON`. The JSON document must be an
 * object and must carry string-typed `type`, `challenge` and `origin` fields.
 */
export function parseClientDataJSON(input: BytesInput): ParsedClientData {
  const raw = toBytes(input);
  if (raw.length === 0) {
    throw new ClientDataError("clientDataJSON is empty");
  }

  let value: unknown;
  try {
    value = JSON.parse(utf8Decode(raw)) as unknown;
  } catch (cause) {
    throw new ClientDataError("clientDataJSON is not valid UTF-8 JSON", { cause });
  }

  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ClientDataError("clientDataJSON must encode a JSON object");
  }

  const documentObject = value as Record<string, unknown>;
  const { type, challenge, origin } = documentObject;

  if (typeof type !== "string" || type.length === 0) {
    throw new ClientDataError('clientDataJSON "type" must be a non-empty string');
  }
  if (typeof challenge !== "string" || challenge.length === 0) {
    throw new ClientDataError('clientDataJSON "challenge" must be a non-empty base64url string');
  }
  if (typeof origin !== "string" || origin.length === 0) {
    throw new ClientDataError('clientDataJSON "origin" must be a non-empty string');
  }
  if (
    "crossOrigin" in documentObject &&
    documentObject.crossOrigin !== undefined &&
    typeof documentObject.crossOrigin !== "boolean"
  ) {
    throw new ClientDataError('clientDataJSON "crossOrigin" must be a boolean when present');
  }

  return {
    raw,
    type,
    challenge,
    origin,
    crossOrigin: documentObject.crossOrigin === true,
    tokenBinding: documentObject.tokenBinding,
  };
}

/** Compare a raw challenge against the base64url-encoded value in clientDataJSON. */
export function challengeMatches(expected: Uint8Array, observed: string): boolean {
  let expectedEncoding: string;
  try {
    expectedEncoding = base64UrlEncode(expected);
  } catch {
    return false;
  }
  if (expectedEncoding === observed) {
    return true;
  }
  // Some platforms historically emit base64 rather than base64url.
  const paddedBase64 = base64Encode(expected);
  return paddedBase64 === observed;
}

/** Build a clientDataJSON payload; intended for tests and host-side helpers. */
export function encodeClientDataJSON(fields: {
  type: string;
  challenge: BytesInput;
  origin: string;
  crossOrigin?: boolean;
}): Uint8Array {
  const documentObject: Record<string, unknown> = {
    type: fields.type,
    challenge: base64UrlEncode(fields.challenge),
    origin: fields.origin,
  };
  if (fields.crossOrigin !== undefined) {
    documentObject.crossOrigin = fields.crossOrigin;
  }
  return new TextEncoder().encode(JSON.stringify(documentObject));
}
