/**
 * tANS table construction.
 *
 * The whole entropy state is a single integer `x` kept in [M, 2M) where
 * M = 2^tableLog is the number of discrete states. Each symbol `s` owns
 * `freq[s]` of the M table slots.
 *
 * Construction (FSE-style):
 * - Symbols are SPREAD across the slot range [0, M) with the classic
 *   stride `(M>>1) + (M>>3) + 3` walk, so post-encode states mix over the
 *   whole state space. (Consecutive slot assignment leaves a constant
 *   ~0.04 bit/symbol redundancy; spreading removes it.)
 * - The occurrence of rank j (0-based) of symbol s maps to predecessor base
 *   y = freq[s] + j with nb = tableLog + 1 - bitLength(y) read-back bits, so
 *   decoding jumps to state (y << nb) + bits. The intervals
 *   [y<<nb, (y+1)<<nb) for y in [f, 2f) tile [M, 2M) exactly, which makes
 *   the encoder the exact inverse of the decoder.
 *
 * Coding one symbol is a single table lookup plus one variable-length bit
 * flush — O(1) per symbol.
 */

export interface EncodeEntry {
  freq: number;
  /** tableLog + 1 - bitLength(freq): bits flushed when state >= threshold */
  shift: number;
  /** freq << shift; states below this flush one bit fewer */
  threshold: number;
  /** slots[j] = M + spread position of this symbol's rank-j occurrence */
  slots: Uint32Array;
}

export interface DecodeEntry {
  symbol: number;
  /** state after decoding, before renormalisation: y << numBits */
  nextState: number;
  /** bits to read back from the stream */
  numBits: number;
}

export interface TansTable {
  tableLog: number;
  size: number;
  symbols: number[];
  freq: Uint32Array;
  /** indexed by symbol value; undefined for symbols not in the alphabet */
  encode: (EncodeEntry | undefined)[];
  decode: DecodeEntry[];
}

function bitLength(v: number): number {
  return 32 - Math.clz32(v);
}

/**
 * Normalise raw symbol counts so they sum to exactly 2^tableLog.
 * Largest-remainder method with a guarantee that every present symbol keeps
 * at least one state.
 */
export function normalizeFrequencies(
  counts: ReadonlyMap<number, number>,
  tableLog: number,
): Uint32Array {
  const size = 1 << tableLog;
  const entries = [...counts.entries()].filter(([, c]) => c > 0);
  if (entries.length === 0) throw new Error("no symbols to model");
  if (entries.length > size) {
    throw new Error(
      `alphabet (${entries.length}) larger than table (${size}); raise tableLog`,
    );
  }
  const total = entries.reduce((a, [, c]) => a + c, 0);

  let maxSym = 0;
  for (const [s] of entries) if (s > maxSym) maxSym = s;
  const norm = new Uint32Array(maxSym + 1);

  const remainders: { s: number; r: number }[] = [];
  let assigned = 0;
  for (const [s, c] of entries) {
    const exact = (c / total) * size;
    const f = Math.max(1, Math.floor(exact));
    norm[s] = f;
    assigned += f;
    remainders.push({ s, r: exact - Math.floor(exact) });
  }

  // Distribute the leftover states to the largest remainders.
  remainders.sort((a, b) => b.r - a.r);
  let i = 0;
  while (assigned < size) {
    norm[remainders[i % remainders.length].s]++;
    assigned++;
    i++;
  }
  // If we overshot (many symbols clamped to 1), shave states from the most
  // frequent symbols, never dropping anyone to zero.
  while (assigned > size) {
    let best = -1;
    for (const [s] of entries) {
      if (norm[s] > 1 && (best < 0 || norm[s] > norm[best])) best = s;
    }
    if (best < 0) throw new Error("cannot normalise frequencies");
    norm[best]--;
    assigned--;
  }
  return norm;
}

/**
 * Build encode/decode tables from normalised frequencies.
 */
export function buildTable(freq: Uint32Array, tableLog: number): TansTable {
  const size = 1 << tableLog;
  const symbols: number[] = [];
  for (let s = 0; s < freq.length; s++) if (freq[s] > 0) symbols.push(s);

  let total = 0;
  for (const s of symbols) total += freq[s];
  if (total !== size) {
    throw new Error(`frequencies sum to ${total}, expected ${size}`);
  }

  // Spread symbol occurrences across [0, M) with an odd stride (full cycle).
  const spread = new Uint32Array(size);
  const step = (size >> 1) + (size >> 3) + 3;
  let pos = 0;
  for (const s of symbols) {
    for (let i = 0; i < freq[s]; i++) {
      spread[pos] = s;
      pos = (pos + step) & (size - 1);
    }
  }

  const encode: (EncodeEntry | undefined)[] = new Array(freq.length);
  const decode: DecodeEntry[] = new Array(size);
  const rank = new Uint32Array(freq.length);
  const slotsBySymbol = new Map<number, Uint32Array>();
  for (const s of symbols) slotsBySymbol.set(s, new Uint32Array(freq[s]));

  for (pos = 0; pos < size; pos++) {
    const s = spread[pos];
    const f = freq[s];
    const j = rank[s]++;
    slotsBySymbol.get(s)![j] = pos;
    const y = f + j; // predecessor base in [f, 2f)
    const nb = tableLog + 1 - bitLength(y);
    decode[pos] = { symbol: s, nextState: y << nb, numBits: nb };
  }

  for (const s of symbols) {
    const f = freq[s];
    const shift = tableLog + 1 - bitLength(f);
    const rawSlots = slotsBySymbol.get(s)!;
    const slots = new Uint32Array(f);
    for (let j = 0; j < f; j++) slots[j] = size + rawSlots[j];
    encode[s] = { freq: f, shift, threshold: f << shift, slots };
  }

  return { tableLog, size, symbols, freq, encode, decode };
}

/** Count symbol occurrences of a byte buffer. */
export function countSymbols(data: Uint8Array): Map<number, number> {
  const counts = new Map<number, number>();
  for (const b of data) counts.set(b, (counts.get(b) ?? 0) + 1);
  return counts;
}
