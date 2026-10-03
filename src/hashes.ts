import { createHash } from "node:crypto";

/** A 32-bit hash function over raw key bytes. */
export type HashFn = (key: Uint8Array) => number;

const textEncoder = new TextEncoder();

export function encodeKey(key: string): Uint8Array {
  return textEncoder.encode(key);
}

/**
 * MD5 truncated to the low 32 bits (little-endian) of the digest.
 * Reference cryptographic mixing function: excellent avalanche behaviour.
 */
export const md5_32: HashFn = (key) => {
  const digest = createHash("md5").update(key).digest();
  return digest.readUInt32LE(0);
};

/** Full hex MD5, exposed for test vectors. */
export function md5Hex(key: string): string {
  return createHash("md5").update(key).digest("hex");
}

/**
 * MurmurHash3 x86 32-bit (Austin Appleby's reference algorithm).
 */
export function murmur3_32(key: Uint8Array, seed = 0): number {
  const c1 = 0xcc9e2d51;
  const c2 = 0x1b873593;
  let h1 = seed >>> 0;
  const len = key.length;
  const nblocks = len >>> 2;

  for (let i = 0; i < nblocks; i++) {
    const o = i * 4;
    let k1 = (key[o] | (key[o + 1] << 8) | (key[o + 2] << 16) | (key[o + 3] << 24)) >>> 0;
    k1 = Math.imul(k1, c1);
    k1 = ((k1 << 15) | (k1 >>> 17)) >>> 0;
    k1 = Math.imul(k1, c2);
    h1 ^= k1;
    h1 = ((h1 << 13) | (h1 >>> 19)) >>> 0;
    h1 = (Math.imul(h1, 5) + 0xe6546b64) | 0;
  }

  const tail = nblocks * 4;
  let k1 = 0;
  switch (len & 3) {
    case 3:
      k1 ^= key[tail + 2] << 16;
    // fallthrough
    case 2:
      k1 ^= key[tail + 1] << 8;
    // fallthrough
    case 1:
      k1 ^= key[tail];
      k1 = Math.imul(k1, c1);
      k1 = ((k1 << 15) | (k1 >>> 17)) >>> 0;
      k1 = Math.imul(k1, c2);
      h1 ^= k1;
  }

  h1 ^= len;
  h1 ^= h1 >>> 16;
  h1 = Math.imul(h1, 0x85ebca6b);
  h1 ^= h1 >>> 13;
  h1 = Math.imul(h1, 0xc2b2ae35);
  h1 ^= h1 >>> 16;
  return h1 >>> 0;
}

/**
 * FNV-1a 32-bit. Fast, but multiplicative diffusion only propagates
 * upward through the word: the low bits see little cross-bit mixing,
 * which is the classic avalanche weakness this analyzer detects.
 */
export const fnv1a_32: HashFn = (key) => {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h ^= key[i];
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
};

/** FNV-1 32-bit (multiply-then-xor), included for comparison. */
export const fnv1_32: HashFn = (key) => {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h = Math.imul(h, 0x01000193);
    h ^= key[i];
  }
  return h >>> 0;
};

export interface HashSpec {
  name: string;
  fn: HashFn;
}

export const HASH_REGISTRY: HashSpec[] = [
  { name: "MD5-32", fn: md5_32 },
  { name: "MurmurHash3-x86-32", fn: murmur3_32 },
  { name: "FNV-1a-32", fn: fnv1a_32 },
];
