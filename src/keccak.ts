const MASK_64 = (1n << 64n) - 1n;
const RATE_BYTES = 136;

const ROUND_CONSTANTS: readonly bigint[] = [
  0x0000000000000001n, 0x0000000000008082n, 0x800000000000808an,
  0x8000000080008000n, 0x000000000000808bn, 0x0000000080000001n,
  0x8000000080008081n, 0x8000000000008009n, 0x000000000000008an,
  0x0000000000000088n, 0x0000000080008009n, 0x000000008000000an,
  0x000000008000808bn, 0x800000000000008bn, 0x8000000000008089n,
  0x8000000000008003n, 0x8000000000008002n, 0x8000000000000080n,
  0x000000000000800an, 0x800000008000000an, 0x8000000080008081n,
  0x8000000000008080n, 0x0000000080000001n, 0x8000000080008008n,
];

const ROTATION_OFFSETS: readonly (readonly number[])[] = [
  [0, 36, 3, 41, 18],
  [1, 44, 10, 45, 2],
  [62, 6, 43, 15, 61],
  [28, 55, 25, 21, 56],
  [27, 20, 39, 8, 14],
];

function rotateLeft(value: bigint, shift: number): bigint {
  if (shift === 0) return value & MASK_64;
  const bits = BigInt(shift);
  return ((value << bits) | (value >> (64n - bits))) & MASK_64;
}

function keccakF1600(state: bigint[]): void {
  const column = new Array<bigint>(5);
  const permuted = new Array<bigint>(25);

  for (let round = 0; round < 24; round += 1) {
    for (let x = 0; x < 5; x += 1) {
      column[x] =
        state[x] ^
        state[x + 5] ^
        state[x + 10] ^
        state[x + 15] ^
        state[x + 20];
    }

    for (let x = 0; x < 5; x += 1) {
      const delta =
        column[(x + 4) % 5] ^ rotateLeft(column[(x + 1) % 5], 1);
      for (let y = 0; y < 5; y += 1) {
        state[x + 5 * y] = (state[x + 5 * y] ^ delta) & MASK_64;
      }
    }

    for (let x = 0; x < 5; x += 1) {
      for (let y = 0; y < 5; y += 1) {
        permuted[y + 5 * ((2 * x + 3 * y) % 5)] = rotateLeft(
          state[x + 5 * y],
          ROTATION_OFFSETS[x][y],
        );
      }
    }

    for (let x = 0; x < 5; x += 1) {
      for (let y = 0; y < 5; y += 1) {
        const index = x + 5 * y;
        state[index] =
          (permuted[index] ^
            (~permuted[(x + 1) % 5 + 5 * y] &
              permuted[(x + 2) % 5 + 5 * y])) &
          MASK_64;
      }
    }

    state[0] = (state[0] ^ ROUND_CONSTANTS[round]) & MASK_64;
  }
}

/**
 * A body may be supplied as the exact bytes received on the wire, or as a
 * parsed JSON value. Strings are hashed as UTF-8 bytes without parsing.
 */
export type HashableBody =
  | string
  | Uint8Array
  | ArrayBuffer
  | readonly unknown[]
  | Record<string, unknown>;

export function bodyToBytes(body: HashableBody): Uint8Array {
  if (typeof body === "string") {
    return new TextEncoder().encode(body);
  }

  if (body instanceof Uint8Array) {
    return body;
  }

  if (body instanceof ArrayBuffer) {
    return new Uint8Array(body);
  }

  if (body !== null && typeof body === "object") {
    const json = JSON.stringify(body);
    if (json === undefined) {
      throw new TypeError("Body is not JSON serializable");
    }
    return new TextEncoder().encode(json);
  }

  throw new TypeError("Body must be a string, byte array, or JSON value");
}

export function keccak256Bytes(input: Uint8Array): Uint8Array {
  const inputLength = input.length;
  const paddedLength = Math.ceil((inputLength + 1) / RATE_BYTES) * RATE_BYTES;
  const padded = new Uint8Array(paddedLength);
  padded.set(input);

  // Keccak padding (pad10*1), not the SHA-3 domain-separated padding.
  padded[inputLength] ^= 0x01;
  padded[paddedLength - 1] ^= 0x80;

  const state = new Array<bigint>(25).fill(0n);

  for (let offset = 0; offset < paddedLength; offset += RATE_BYTES) {
    for (let index = 0; index < RATE_BYTES; index += 1) {
      const byte = padded[offset + index];
      const lane = index >> 3;
      const shift = BigInt((index & 7) * 8);
      state[lane] = (state[lane] ^ (BigInt(byte) << shift)) & MASK_64;
    }
    keccakF1600(state);
  }

  const output = new Uint8Array(32);
  for (let index = 0; index < output.length; index += 1) {
    const lane = index >> 3;
    const shift = BigInt((index & 7) * 8);
    output[index] = Number((state[lane] >> shift) & 0xffn);
  }

  return output;
}

export function bytesToHex(bytes: Uint8Array): string {
  let hex = "";
  for (const byte of bytes) {
    hex += byte.toString(16).padStart(2, "0");
  }
  return `0x${hex}`;
}

/**
 * Returns an Ethereum-style, 0x-prefixed Keccak-256 digest.
 */
export function keccak256(body: HashableBody): string {
  return bytesToHex(keccak256Bytes(bodyToBytes(body)));
}

export const keccak256Hex = keccak256;
