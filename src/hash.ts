const MASK_64 = (1n << 64n) - 1n;

const ROUND_CONSTANTS = [
  0x0000000000000001n,
  0x0000000000008082n,
  0x800000000000808an,
  0x8000000080008000n,
  0x000000000000808bn,
  0x0000000080000001n,
  0x8000000080008081n,
  0x8000000000008009n,
  0x000000000000008an,
  0x0000000000000088n,
  0x0000000080008009n,
  0x000000008000000an,
  0x000000008000808bn,
  0x800000000000008bn,
  0x8000000000008089n,
  0x8000000000008003n,
  0x8000000000008002n,
  0x8000000000000080n,
  0x000000000000800an,
  0x800000008000000an,
  0x8000000080008081n,
  0x8000000000008080n,
  0x0000000080000001n,
  0x8000000080008008n,
] as const;

const ROTATION_OFFSETS = [
  0, 1, 62, 28, 27,
  36, 44, 6, 55, 20,
  3, 10, 43, 25, 39,
  41, 45, 15, 21, 8,
  18, 2, 61, 56, 14,
] as const;

const KECCAK_RATE = 136;
const KECCAK_OUTPUT_BYTES = 32;

function rotateLeft(value: bigint, shift: number): bigint {
  if (shift === 0) {
    return value & MASK_64;
  }
  const amount = BigInt(shift);
  return ((value << amount) | (value >> (64n - amount))) & MASK_64;
}

function keccakPermutation(state: bigint[]): void {
  const column = new Array<bigint>(5);
  const parity = new Array<bigint>(5);
  const rotated = new Array<bigint>(25);

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
      parity[x] = column[(x + 4) % 5] ^ rotateLeft(column[(x + 1) % 5], 1);
    }

    for (let y = 0; y < 5; y += 1) {
      for (let x = 0; x < 5; x += 1) {
        state[x + 5 * y] = (state[x + 5 * y] ^ parity[x]) & MASK_64;
      }
    }

    for (let y = 0; y < 5; y += 1) {
      for (let x = 0; x < 5; x += 1) {
        const destinationX = y;
        const destinationY = (2 * x + 3 * y) % 5;
        rotated[destinationX + 5 * destinationY] = rotateLeft(
          state[x + 5 * y],
          ROTATION_OFFSETS[x + 5 * y],
        );
      }
    }

    for (let y = 0; y < 5; y += 1) {
      for (let x = 0; x < 5; x += 1) {
        const lane = x + 5 * y;
        const next = ((x + 1) % 5) + 5 * y;
        const nextNext = ((x + 2) % 5) + 5 * y;
        state[lane] =
          (rotated[lane] ^ ((~rotated[next] & MASK_64) & rotated[nextNext])) &
          MASK_64;
      }
    }

    state[0] = (state[0] ^ ROUND_CONSTANTS[round]) & MASK_64;
  }
}

export function keccak256(data: Uint8Array): Uint8Array {
  const paddedLength =
    Math.floor((data.length + KECCAK_RATE) / KECCAK_RATE) * KECCAK_RATE;
  const padded = new Uint8Array(paddedLength);
  padded.set(data);
  padded[data.length] ^= 0x01;
  padded[paddedLength - 1] ^= 0x80;

  const state = new Array<bigint>(25).fill(0n);

  for (let offset = 0; offset < padded.length; offset += KECCAK_RATE) {
    for (let lane = 0; lane < KECCAK_RATE / 8; lane += 1) {
      let value = 0n;
      for (let byte = 0; byte < 8; byte += 1) {
        value |= BigInt(padded[offset + lane * 8 + byte]) << BigInt(byte * 8);
      }
      state[lane] ^= value;
    }
    keccakPermutation(state);
  }

  const output = new Uint8Array(KECCAK_OUTPUT_BYTES);
  for (let lane = 0; lane < KECCAK_OUTPUT_BYTES / 8; lane += 1) {
    let value = state[lane];
    for (let byte = 0; byte < 8; byte += 1) {
      output[lane * 8 + byte] = Number(value & 0xffn);
      value >>= 8n;
    }
  }
  return output;
}

export function bytesToHex(bytes: Uint8Array): string {
  let result = "0x";
  for (const byte of bytes) {
    result += byte.toString(16).padStart(2, "0");
  }
  return result;
}

export function hexToBytes(value: string): Uint8Array {
  if (typeof value !== "string" || !/^0x(?:[0-9a-fA-F]{2})*$/.test(value)) {
    throw new TypeError("Expected an even-length 0x-prefixed hex string");
  }
  const bytes = new Uint8Array((value.length - 2) / 2);
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = Number.parseInt(value.slice(2 + index * 2, 4 + index * 2), 16);
  }
  return bytes;
}

export function utf8Bytes(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

export function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const totalLength = parts.reduce((sum, part) => sum + part.length, 0);
  const combined = new Uint8Array(totalLength);
  let offset = 0;
  for (const part of parts) {
    combined.set(part, offset);
    offset += part.length;
  }
  return combined;
}

export function keccak256Hex(data: Uint8Array): string {
  return bytesToHex(keccak256(data));
}

export function isHex(value: unknown): value is string {
  return typeof value === "string" && /^0x[0-9a-fA-F]*$/.test(value);
}

export function isBytes32(value: unknown): value is string {
  return typeof value === "string" && /^0x[0-9a-fA-F]{64}$/.test(value);
}

export function isAddress(value: unknown): value is string {
  return typeof value === "string" && /^0x[0-9a-fA-F]{40}$/.test(value);
}
