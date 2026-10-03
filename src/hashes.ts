import { createHash } from 'node:crypto';

/** 哈希输入：字符串或原始字节 */
export type Key = string | Uint8Array;

/** 统一输出 uint32 的哈希函数签名 */
export type HashFn = (key: Key) => number;

export function toBytes(key: Key): Uint8Array {
  return typeof key === 'string' ? Buffer.from(key, 'utf8') : key;
}

/** MD5（取摘要大端前 4 字节） */
export function md5_32(key: Key): number {
  const digest = createHash('md5').update(toBytes(key)).digest();
  return digest.readUInt32BE(0);
}

/** MurmurHash3 x86_32 */
export function murmur3_32(key: Key, seed = 0): number {
  const data = toBytes(key);
  const len = data.length;
  let h1 = seed >>> 0;
  const c1 = 0xcc9e2d51;
  const c2 = 0x1b873593;

  const nblocks = len >>> 2;
  for (let i = 0; i < nblocks; i++) {
    const off = i * 4;
    let k1 =
      (data[off] |
        (data[off + 1] << 8) |
        (data[off + 2] << 16) |
        (data[off + 3] << 24)) >>>
      0;
    k1 = Math.imul(k1, c1);
    k1 = (k1 << 15) | (k1 >>> 17);
    k1 = Math.imul(k1, c2);

    h1 ^= k1;
    h1 = (h1 << 13) | (h1 >>> 19);
    h1 = (Math.imul(h1, 5) + 0xe6546b64) >>> 0;
  }

  let k1 = 0;
  const tail = nblocks * 4;
  switch (len & 3) {
    case 3:
      k1 ^= data[tail + 2] << 16;
    // fallthrough
    case 2:
      k1 ^= data[tail + 1] << 8;
    // fallthrough
    case 1:
      k1 ^= data[tail];
      k1 = Math.imul(k1, c1);
      k1 = (k1 << 15) | (k1 >>> 17);
      k1 = Math.imul(k1, c2);
      h1 ^= k1;
  }

  h1 ^= len;
  // fmix32
  h1 ^= h1 >>> 16;
  h1 = Math.imul(h1, 0x85ebca6b);
  h1 ^= h1 >>> 13;
  h1 = Math.imul(h1, 0xc2b2ae35);
  h1 ^= h1 >>> 16;
  return h1 >>> 0;
}

/** FNV-1a 32 位 */
export function fnv1a_32(key: Key): number {
  const data = toBytes(key);
  let h = 0x811c9dc5;
  for (let i = 0; i < data.length; i++) {
    h ^= data[i];
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

export type TruncateMode = 'low' | 'high';

/**
 * 位宽截断：模拟哈希输出仅取低 bits 位或高 bits 位，
 * 用于暴露特定位宽下雪崩效应不足的缺陷。
 */
export function truncateBits(fn: HashFn, bits: number, mode: TruncateMode = 'low'): HashFn {
  if (bits < 1 || bits > 32) throw new RangeError(`bits must be in [1,32], got ${bits}`);
  if (bits === 32) return fn;
  if (mode === 'low') {
    const mask = (1 << bits) - 1;
    return (key) => fn(key) & mask;
  }
  const shift = 32 - bits;
  return (key) => fn(key) >>> shift;
}

export interface HashEntry {
  name: string;
  fn: HashFn;
}

export const HASHES: HashEntry[] = [
  { name: 'MD5-32', fn: md5_32 },
  { name: 'Murmur3-32', fn: murmur3_32 },
  { name: 'FNV1a-32', fn: fnv1a_32 },
];
