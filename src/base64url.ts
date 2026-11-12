/**
 * Unpadded base64url helpers (RFC 7515, Appendix C). No Buffer dependency so
 * the same code works in browsers and other runtimes.
 */

const BASE64URL_CHARS =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
const DECODE_TABLE: Record<string, number> = {};
for (let index = 0; index < BASE64URL_CHARS.length; index += 1) {
  DECODE_TABLE[BASE64URL_CHARS[index]] = index;
}

function assertB64UrlChars(encoded: string): void {
  if (encoded.length === 0 || /[^A-Za-z0-9-_]/.test(encoded)) {
    throw new TypeError("Invalid base64url encoding");
  }
}

export function b64urlEncode(input: Uint8Array | string): string {
  const bytes =
    typeof input === "string" ? new TextEncoder().encode(input) : input;
  let output = "";

  for (let offset = 0; offset < bytes.length; offset += 3) {
    const chunk =
      (bytes[offset] << 16) |
      ((bytes[offset + 1] ?? 0) << 8) |
      (bytes[offset + 2] ?? 0);
    output += BASE64URL_CHARS[(chunk >> 18) & 63];
    output += BASE64URL_CHARS[(chunk >> 12) & 63];
    if (offset + 1 < bytes.length) {
      output += BASE64URL_CHARS[(chunk >> 6) & 63];
    }
    if (offset + 2 < bytes.length) {
      output += BASE64URL_CHARS[chunk & 63];
    }
  }

  return output;
}

export function b64urlDecode(encoded: string): Uint8Array {
  assertB64UrlChars(encoded);
  const length = encoded.length;
  const remainder = length % 4;
  const byteLength =
    Math.floor(length / 4) * 3 +
    (remainder === 3 ? 2 : remainder === 2 ? 1 : 0);
  const bytes = new Uint8Array(byteLength);

  let writeOffset = 0;
  for (let offset = 0; offset < length; offset += 4) {
    const c0 = DECODE_TABLE[encoded[offset]];
    const c1 = DECODE_TABLE[encoded[offset + 1]];
    const c2 = DECODE_TABLE[encoded[offset + 2]];
    const c3 = DECODE_TABLE[encoded[offset + 3]];

    const triple = (c0 << 18) | (c1 << 12) | ((c2 ?? 0) << 6) | (c3 ?? 0);
    bytes[writeOffset] = (triple >> 16) & 255;
    if (offset + 2 < length) {
      bytes[writeOffset + 1] = (triple >> 8) & 255;
    }
    if (offset + 3 < length) {
      bytes[writeOffset + 2] = triple & 255;
    }
    writeOffset += 3;
  }

  return bytes;
}

export function b64urlDecodeString(encoded: string): string {
  return new TextDecoder().decode(b64urlDecode(encoded));
}

export function b64urlDecodeJson<T>(encoded: string): T {
  let parsed: unknown;
  try {
    parsed = JSON.parse(b64urlDecodeString(encoded));
  } catch {
    throw new TypeError("Encoded value is not valid JSON");
  }
  return parsed as T;
}
