/**
 * 纯 TypeScript 的 Keccak-256 实现（以太坊采用的原始 Keccak，
 * 填充域字节为 0x01，而非 SHA3 的 0x06）。仅供测试与参考，
 * 生产代码应由调用方注入经过审计的实现。
 * 置换步骤对应参考实现 keccak.c 的查表算法（rotc / piln）。
 */

const RATE_BYTES = 136; // 1088 位
const OUTPUT_BYTES = 32;
const MASK64 = 0xffffffffffffffffn;

const ROUND_CONSTANTS = [
  0x0000000000000001n, 0x0000000000008082n, 0x800000000000808an,
  0x8000000080008000n, 0x000000000000808bn, 0x0000000080000001n,
  0x8000000080008081n, 0x8000000000008009n, 0x000000000000008an,
  0x0000000000000088n, 0x0000000080008009n, 0x000000008000000an,
  0x000000008000808bn, 0x800000000000008bn, 0x8000000000008089n,
  0x8000000000008003n, 0x8000000000008002n, 0x8000000000000080n,
  0x000000000000800an, 0x800000008000000an, 0x8000000080008081n,
  0x8000000000008080n, 0x0000000080000001n, 0x8000000080008008n,
];

// 每个 lane（索引 x + 5y）的 ρ 旋转量，按 Keccak 规范
// r = ((t+1)(t+2)/2) mod 64 与坐标链 (x,y) -> (y, 2x+3y mod 5) 预计算。
const ROTATION_BY_LANE = [
  0, 1, 62, 28, 27, 36, 44, 6, 55, 20, 3, 10, 43, 25, 39, 41, 45, 15, 21,
  8, 18, 2, 61, 56, 14,
];

function rotl64(value: bigint, bits: number): bigint {
  if (bits === 0) return value;
  return ((value << BigInt(bits)) | (value >> BigInt(64 - bits))) & MASK64;
}

function keccakF(state: bigint[]): void {
  const bc = new Array<bigint>(5).fill(0n);

  for (let round = 0; round < 24; round += 1) {
    // θ
    for (let i = 0; i < 5; i += 1) {
      bc[i] = state[i] ^ state[i + 5] ^ state[i + 10] ^ state[i + 15] ^ state[i + 20];
    }
    for (let i = 0; i < 5; i += 1) {
      const t0 = bc[(i + 4) % 5] ^ rotl64(bc[(i + 1) % 5], 1);
      for (let j = 0; j < 25; j += 5) {
        state[j + i] ^= t0;
      }
    }

    // ρ 与 π：B[y][2x+3y mod 5] = rot(A[x][y], r[x][y])，
    // 沿规范坐标链遍历 24 个非零 lane。
    const b = new Array<bigint>(25).fill(0n);
    b[0] = state[0];
    let x = 1;
    let y = 0;
    for (let step = 0; step < 24; step += 1) {
      const source = x + 5 * y;
      const destinationX = y;
      const destinationY = (2 * x + 3 * y) % 5;
      b[destinationX + 5 * destinationY] = rotl64(
        state[source],
        ROTATION_BY_LANE[source],
      );
      const nextX = y;
      const nextY = (2 * x + 3 * y) % 5;
      x = nextX;
      y = nextY;
    }

    // χ
    for (let j = 0; j < 25; j += 5) {
      for (let i = 0; i < 5; i += 1) {
        bc[i] = b[j + i];
      }
      for (let i = 0; i < 5; i += 1) {
        b[j + i] =
          bc[i] ^ ((~bc[(i + 1) % 5] & MASK64) & bc[(i + 2) % 5]);
      }
    }

    // ι
    b[0] ^= ROUND_CONSTANTS[round];
    for (let i = 0; i < 25; i += 1) {
      state[i] = b[i];
    }
  }
}

/** Keccak-256，输入任意字节，输出 32 字节哈希。 */
export function keccak256(input: Uint8Array): Uint8Array {
  const state = new Array<bigint>(25).fill(0n);

  // 多一速率填充：0x01 ... 0x80（Keccak 原始域分隔）。
  const paddedLength = Math.ceil((input.length + 1) / RATE_BYTES) * RATE_BYTES;
  const padded = new Uint8Array(paddedLength);
  padded.set(input, 0);
  padded[input.length] = 0x01;
  padded[paddedLength - 1] |= 0x80;

  for (let blockStart = 0; blockStart < padded.length; blockStart += RATE_BYTES) {
    for (let lane = 0; lane < RATE_BYTES / 8; lane += 1) {
      let word = 0n;
      const base = blockStart + lane * 8;
      for (let byteIndex = 7; byteIndex >= 0; byteIndex -= 1) {
        word = (word << 8n) | BigInt(padded[base + byteIndex]);
      }
      state[lane] ^= word;
    }
    keccakF(state);
  }

  const output = new Uint8Array(OUTPUT_BYTES);
  let written = 0;
  let laneIndex = 0;
  while (written < OUTPUT_BYTES) {
    let word = state[laneIndex];
    for (let byteIndex = 0; byteIndex < 8 && written < OUTPUT_BYTES; byteIndex += 1) {
      output[written] = Number(word & 0xffn);
      word >>= 8n;
      written += 1;
    }
    laneIndex += 1;
  }
  return output;
}
