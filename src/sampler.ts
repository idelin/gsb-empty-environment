/** Deterministic PRNG (mulberry32) so analysis runs are reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export type KeyGenerator = (index: number, rand: () => number) => string;

/** Random decimal-suffixed cache keys, e.g. "user:482910374". */
export const randomUserKeys: KeyGenerator = (_i, rand) =>
  `user:${Math.floor(rand() * 1_000_000_000)}`;

/** Sequential cache keys, e.g. "user:12345". Worst case for weak avalanche. */
export const sequentialUserKeys: KeyGenerator = (i) => `user:${i}`;

/** Random hex session tokens, e.g. "sess:9f3ac1b07e52d480". */
export const randomHexKeys: KeyGenerator = (_i, rand) => {
  let s = "";
  for (let k = 0; k < 16; k++) s += Math.floor(rand() * 16).toString(16);
  return `sess:${s}`;
};

export const KEY_GENERATORS: Record<string, KeyGenerator> = {
  random: randomUserKeys,
  sequential: sequentialUserKeys,
  hex: randomHexKeys,
};
