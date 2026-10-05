// Greedy hash-chain LZ77 tokenizer. This is the compressor-side match logic;
// the decoder and the reference expander never use it.

import { MAX_MATCH, MIN_MATCH } from './format.ts';
import type { Token } from './tokens.ts';

const HASH_BITS = 15;
const HASH_SIZE = 1 << HASH_BITS;
const NIL = -1;
const MAX_CHAIN = 128;

export function lz77Tokens(data: Uint8Array, windowBits: number): Token[] {
  const n = data.length;
  const tokens: Token[] = [];
  if (n === 0) return tokens;

  const window = 1 << windowBits;
  const head = new Int32Array(HASH_SIZE).fill(NIL);
  const prev = new Int32Array(n).fill(NIL);

  const hashAt = (i: number): number =>
    ((data[i] << 10) ^ (data[i + 1] << 5) ^ data[i + 2]) & (HASH_SIZE - 1);

  const insert = (i: number): void => {
    if (i + MIN_MATCH <= n) {
      const h = hashAt(i);
      prev[i] = head[h];
      head[h] = i;
    }
  };

  let i = 0;
  while (i < n) {
    let bestLen = 0;
    let bestDist = 0;
    if (i + MIN_MATCH <= n) {
      const maxLen = Math.min(MAX_MATCH, n - i);
      let cand = head[hashAt(i)];
      let chain = 0;
      while (cand !== NIL && i - cand <= window && chain < MAX_CHAIN) {
        chain++;
        if (bestLen === 0 || data[cand + bestLen] === data[i + bestLen]) {
          let l = 0;
          while (l < maxLen && data[cand + l] === data[i + l]) l++;
          if (l > bestLen) {
            bestLen = l;
            bestDist = i - cand;
            if (l >= maxLen) break;
          }
        }
        cand = prev[cand];
      }
    }
    if (bestLen >= MIN_MATCH) {
      tokens.push({ type: 'match', length: bestLen, distance: bestDist });
      const end = i + bestLen;
      while (i < end) {
        insert(i);
        i++;
      }
    } else {
      tokens.push({ type: 'literal', byte: data[i] });
      insert(i);
      i++;
    }
  }
  return tokens;
}
