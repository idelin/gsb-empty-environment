import { Sha256 } from "./sha256.ts";

const BLOCK_SIZE = 64;

/**
 * Streaming HMAC-SHA256 (RFC 2104) built on the pure-TS SHA-256 so no
 * platform-specific crypto APIs are required.
 */
export class HmacSha256 {
  readonly #inner = new Sha256();
  readonly #outer = new Sha256();

  constructor(key: Uint8Array) {
    let normalizedKey = key;
    if (normalizedKey.length > BLOCK_SIZE) {
      normalizedKey = new Sha256().update(normalizedKey).digest();
    }

    const innerKey = new Uint8Array(BLOCK_SIZE);
    const outerKey = new Uint8Array(BLOCK_SIZE);
    innerKey.set(normalizedKey);
    outerKey.set(normalizedKey);
    for (let index = 0; index < BLOCK_SIZE; index += 1) {
      innerKey[index] ^= 0x36;
      outerKey[index] ^= 0x5c;
    }

    this.#inner.update(innerKey);
    this.#outer.update(outerKey);
  }

  update(data: Uint8Array): this {
    this.#inner.update(data);
    return this;
  }

  digest(): Uint8Array {
    const innerDigest = this.#inner.digest();
    return this.#outer.update(innerDigest).digest();
  }
}

const HEX_DIGITS = "0123456789abcdef";

export function toLowerHex(bytes: Uint8Array): string {
  let output = "";
  for (const byte of bytes) {
    output += HEX_DIGITS[byte >> 4] + HEX_DIGITS[byte & 0x0f];
  }
  return output;
}

/**
 * Parse exactly 64 lowercase hexadecimal characters into 32 bytes.
 * Returns null for wrong length, uppercase, or non-hex characters.
 */
export function parseLowerHex32(value: string): Uint8Array | null {
  if (value.length !== 64) {
    return null;
  }
  const bytes = new Uint8Array(32);
  for (let index = 0; index < 32; index += 1) {
    const high = hexValue(value.charCodeAt(index * 2));
    const low = hexValue(value.charCodeAt(index * 2 + 1));
    if (high < 0 || low < 0) {
      return null;
    }
    bytes[index] = (high << 4) | low;
  }
  return bytes;
}

function hexValue(charCode: number): number {
  if (charCode >= 48 && charCode <= 57) {
    return charCode - 48;
  }
  if (charCode >= 97 && charCode <= 102) {
    return charCode - 87;
  }
  return -1;
}

/**
 * Length-independent constant-time comparison of two equal-length byte
 * arrays. Callers must reject length mismatches before invoking this.
 */
export function constantTimeEqual(
  expected: Uint8Array,
  actual: Uint8Array,
): boolean {
  if (expected.length !== actual.length) {
    return false;
  }
  let difference = 0;
  for (let index = 0; index < expected.length; index += 1) {
    difference |= expected[index] ^ actual[index];
  }
  return difference === 0;
}
