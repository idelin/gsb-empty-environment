// Stage 1: LZ77 tokenization over a 32 KiB sliding window using a hash
// table of 3-byte sequences with chained candidates and lazy matching.

export type Token =
  | { kind: 'literal'; value: number }
  | { kind: 'match'; length: number; distance: number };

export const WINDOW_SIZE = 32768;
export const MIN_MATCH = 3;
export const MAX_MATCH = 258;

const HASH_BITS = 15;
const HASH_SIZE = 1 << HASH_BITS;
const WINDOW_MASK = WINDOW_SIZE - 1;
const NO_POS = -1;

export interface Lz77Options {
  maxChain?: number; // max hash-chain candidates to inspect per position
  niceLength?: number; // stop searching when a match this long is found
}

function hash3(data: Buffer | Uint8Array, i: number): number {
  return ((data[i] * 0x9e37 + data[i + 1] * 0x7f4a + data[i + 2] * 0x2c1b) >>> 0) &
    (HASH_SIZE - 1);
}

export function tokenize(data: Buffer | Uint8Array, options: Lz77Options = {}): Token[] {
  const maxChain = options.maxChain ?? 256;
  const niceLength = options.niceLength ?? 128;
  const n = data.length;
  const tokens: Token[] = [];
  const head = new Int32Array(HASH_SIZE).fill(NO_POS);
  const prev = new Int32Array(WINDOW_SIZE).fill(NO_POS);

  const insert = (i: number): void => {
    if (i + MIN_MATCH > n) return;
    const h = hash3(data, i);
    prev[i & WINDOW_MASK] = head[h];
    head[h] = i;
  };

  const findMatch = (i: number): { length: number; distance: number } => {
    let bestLen = MIN_MATCH - 1;
    let bestDist = 0;
    if (i + MIN_MATCH > n) return { length: 0, distance: 0 };
    const maxLen = Math.min(MAX_MATCH, n - i);
    let candidate = head[hash3(data, i)];
    let chain = maxChain;
    while (candidate !== NO_POS && i - candidate <= WINDOW_SIZE && chain-- > 0) {
      // Quick check on the byte that would extend the current best.
      if (bestLen < maxLen && data[candidate + bestLen] === data[i + bestLen]) {
        let len = 0;
        while (len < maxLen && data[candidate + len] === data[i + len]) len++;
        if (len > bestLen) {
          bestLen = len;
          bestDist = i - candidate;
          if (len >= niceLength || len >= maxLen) break;
        }
      }
      candidate = prev[candidate & WINDOW_MASK];
    }
    return bestLen >= MIN_MATCH
      ? { length: bestLen, distance: bestDist }
      : { length: 0, distance: 0 };
  };

  let i = 0;
  while (i < n) {
    const match = findMatch(i);
    if (match.length >= MIN_MATCH) {
      // Lazy evaluation: emit a literal if the next position matches longer.
      const next = findMatch(i + 1);
      if (next.length > match.length) {
        tokens.push({ kind: 'literal', value: data[i] });
        insert(i);
        i += 1;
        continue;
      }
      tokens.push({ kind: 'match', length: match.length, distance: match.distance });
      for (let k = i; k < i + match.length; k++) insert(k);
      i += match.length;
    } else {
      tokens.push({ kind: 'literal', value: data[i] });
      insert(i);
      i += 1;
    }
  }
  return tokens;
}
