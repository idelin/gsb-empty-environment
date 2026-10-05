import { BlockDecoder } from '../src/block.ts';

export function assertBytesEqual(actual: Uint8Array, expected: Uint8Array): void {
  if (actual.length !== expected.length) {
    throw new Error(`长度不符: ${actual.length} != ${expected.length}`);
  }
  for (let i = 0; i < actual.length; i++) {
    if (actual[i] !== expected[i]) {
      throw new Error(`偏移 ${i}: ${actual[i]} != ${expected[i]}`);
    }
  }
}

// 以给定的输入分块大小序列与输出额度序列做流式解码。
export function decodeStreaming(
  data: Uint8Array,
  chunkSizes: number[],
  quotas: number[],
): Uint8Array {
  const decoder = new BlockDecoder();
  const out: number[] = [];
  let ci = 0;
  let qi = 0;
  let off = 0;
  for (;;) {
    if (off < data.length) {
      const n = Math.min(chunkSizes[ci++ % chunkSizes.length], data.length - off);
      decoder.push(data.subarray(off, off + n));
      off += n;
    }
    const { output, done } = decoder.decode(quotas[qi++ % quotas.length]);
    for (const b of output) out.push(b);
    if (done) break;
    if (off >= data.length && output.length === 0) break; // 无进展
  }
  decoder.finish();
  return Uint8Array.from(out);
}

// 可复现的伪随机数（mulberry32）。
export function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randomBytes(rand: () => number, n: number, alphabet: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = Math.floor(rand() * alphabet) & 0xff;
  return out;
}
