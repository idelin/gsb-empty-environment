export type BytesInput = Uint8Array | ArrayBuffer | ArrayBufferView | string;

const BASE64URL_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

/** Normalize the supported binary/string inputs into a fresh `Uint8Array`. */
export function toBytes(input: BytesInput): Uint8Array {
  if (input instanceof Uint8Array) {
    return new Uint8Array(input);
  }
  if (input instanceof ArrayBuffer) {
    return new Uint8Array(input.slice(0));
  }
  if (ArrayBuffer.isView(input)) {
    const view = input as ArrayBufferView;
    return new Uint8Array(view.buffer, view.byteOffset, view.byteLength).slice();
  }
  if (typeof input === "string") {
    return base64UrlDecode(input);
  }
  throw new TypeError("Expected Uint8Array, ArrayBuffer, ArrayBufferView or base64url string");
}

export function utf8Encode(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

export function utf8Decode(bytes: Uint8Array): string {
  return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
}

export function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((length, part) => length + part.length, 0);
  const output = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    output.set(part, offset);
    offset += part.length;
  }
  return output;
}

export function base64UrlEncode(input: BytesInput): string {
  const bytes = input instanceof Uint8Array ? input : toBytes(input);
  let output = "";
  let i = 0;
  for (; i + 2 < bytes.length; i += 3) {
    const chunk = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];
    output += BASE64URL_CHARS[(chunk >> 18) & 63];
    output += BASE64URL_CHARS[(chunk >> 12) & 63];
    output += BASE64URL_CHARS[(chunk >> 6) & 63];
    output += BASE64URL_CHARS[chunk & 63];
  }
  const remainder = bytes.length - i;
  if (remainder === 1) {
    const chunk = bytes[i] << 16;
    output += BASE64URL_CHARS[(chunk >> 18) & 63];
    output += BASE64URL_CHARS[(chunk >> 12) & 63];
  } else if (remainder === 2) {
    const chunk = (bytes[i] << 16) | (bytes[i + 1] << 8);
    output += BASE64URL_CHARS[(chunk >> 18) & 63];
    output += BASE64URL_CHARS[(chunk >> 12) & 63];
    output += BASE64URL_CHARS[(chunk >> 6) & 63];
  }
  return output;
}

export function base64UrlDecode(input: string): Uint8Array {
  if (typeof input !== "string") {
    throw new TypeError("base64url input must be a string");
  }
  if (input.length === 0) {
    return new Uint8Array(0);
  }
  const lookup = new Int8Array(256).fill(-1);
  for (let index = 0; index < BASE64URL_CHARS.length; index += 1) {
    lookup[BASE64URL_CHARS.charCodeAt(index)] = index;
  }
  lookup["=".charCodeAt(0)] = 0;
  lookup["+".charCodeAt(0)] = 62;
  lookup["/".charCodeAt(0)] = 63;

  const clean = input.replace(/=+$/u, "");
  if (clean.length === 0 || /[^A-Za-z0-9\-_+/]/u.test(clean)) {
    throw new TypeError("Malformed base64url input");
  }
  const outputLength = Math.floor((clean.length * 3) / 4);
  const output = new Uint8Array(outputLength);
  let writeOffset = 0;
  let bits = 0;
  let bitsHeld = 0;
  for (let index = 0; index < clean.length; index += 1) {
    const value = lookup[clean.charCodeAt(index)];
    if (value < 0) {
      throw new TypeError("Malformed base64url input");
    }
    bits = (bits << 6) | value;
    bitsHeld += 6;
    if (bitsHeld >= 8) {
      bitsHeld -= 8;
      output[writeOffset] = (bits >> bitsHeld) & 0xff;
      writeOffset += 1;
    }
  }
  if (writeOffset !== outputLength || bitsHeld >= 6 || (bits & ((1 << bitsHeld) - 1)) !== 0) {
    throw new TypeError("Malformed base64url input");
  }
  return output;
}

const BASE64_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/** Standard base64 with padding, implemented locally to avoid host globals. */
export function base64Encode(input: BytesInput): string {
  const bytes = input instanceof Uint8Array ? input : toBytes(input);
  let output = "";
  let index = 0;
  for (; index + 2 < bytes.length; index += 3) {
    const chunk = (bytes[index] << 16) | (bytes[index + 1] << 8) | bytes[index + 2];
    output += BASE64_CHARS[(chunk >> 18) & 63];
    output += BASE64_CHARS[(chunk >> 12) & 63];
    output += BASE64_CHARS[(chunk >> 6) & 63];
    output += BASE64_CHARS[chunk & 63];
  }
  const remainder = bytes.length - index;
  if (remainder === 1) {
    const chunk = bytes[index] << 16;
    output += BASE64_CHARS[(chunk >> 18) & 63];
    output += BASE64_CHARS[(chunk >> 12) & 63];
    output += "==";
  } else if (remainder === 2) {
    const chunk = (bytes[index] << 16) | (bytes[index + 1] << 8);
    output += BASE64_CHARS[(chunk >> 18) & 63];
    output += BASE64_CHARS[(chunk >> 12) & 63];
    output += BASE64_CHARS[(chunk >> 6) & 63];
    output += "=";
  }
  return output;
}

/** Constant-time comparison for equal-length byte strings. */
export function timingSafeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) {
    return false;
  }
  let mismatch = 0;
  for (let index = 0; index < a.length; index += 1) {
    mismatch |= a[index] ^ b[index];
  }
  return mismatch === 0;
}

export function readUint32Be(bytes: Uint8Array, offset: number): number {
  return (
    ((bytes[offset] << 24) >>> 0) +
    (bytes[offset + 1] << 16) +
    (bytes[offset + 2] << 8) +
    bytes[offset + 3]
  );
}

export function readUint16Be(bytes: Uint8Array, offset: number): number {
  return (bytes[offset] << 8) | bytes[offset + 1];
}
