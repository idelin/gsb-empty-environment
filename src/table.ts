import { ALPHABET_SIZE, highbit } from "./normalize.ts";

/**
 * tANS state-transition tables (FSE-style construction).
 *
 * The whole coding machinery lives in a single integer "state"
 * (tableSize <= state < 2*tableSize on the encoder side) plus these
 * precomputed tables, so both Push and Pop are O(1) table lookups:
 *
 *   encode: nbBits = (state + deltaNbBits[s]) >> 16
 *           emit low nbBits of state
 *           state  = nextState[(state >> nbBits) + deltaFindState[s]]
 *
 *   decode: s     = dSymbol[state]
 *           state = dNewState[state] + readBits(dNbBits[state])
 */
export interface TansTables {
  tableLog: number;
  tableSize: number;
  /** Normalized frequencies, summing to tableSize. */
  norm: Uint32Array;
  /** Encode: next-state table, indexed by (state >> nbBits) + deltaFindState[s]. */
  nextState: Uint16Array;
  /** Encode: per-symbol bit-count bias, (maxBitsOut << 16) - minStatePlus. */
  deltaNbBits: Int32Array;
  /** Encode: per-symbol offset into nextState. */
  deltaFindState: Int32Array;
  /** Decode: symbol emitted at each state. */
  dSymbol: Uint8Array;
  /** Decode: bits to read at each state. */
  dNbBits: Uint8Array;
  /** Decode: baseline of the next state at each state. */
  dNewState: Uint16Array;
}

export function buildTables(norm: Uint32Array, tableLog: number): TansTables {
  const tableSize = 1 << tableLog;

  let sum = 0;
  for (let s = 0; s < ALPHABET_SIZE; s++) sum += norm[s];
  if (sum !== tableSize) {
    throw new Error(`frequencies sum to ${sum}, expected ${tableSize}`);
  }

  // Spread symbols across the state range with an odd step (coprime with
  // 2^tableLog, so every slot is visited exactly once) for a near-uniform
  // distribution of each symbol's states.
  const symbolAt = new Uint8Array(tableSize);
  const step = (tableSize >> 1) + (tableSize >> 3) + 3;
  let pos = 0;
  for (let s = 0; s < ALPHABET_SIZE; s++) {
    for (let i = 0; i < norm[s]; i++) {
      symbolAt[pos] = s;
      pos = (pos + step) & (tableSize - 1);
    }
  }

  // Cumulative frequencies.
  const cumul = new Uint32Array(ALPHABET_SIZE + 1);
  for (let s = 0; s < ALPHABET_SIZE; s++) cumul[s + 1] = cumul[s] + norm[s];

  // Encode table: sub-state -> next encoder state (tableSize + decode slot).
  const nextState = new Uint16Array(tableSize);
  const cursor = cumul.slice(0, ALPHABET_SIZE);
  for (let u = 0; u < tableSize; u++) {
    nextState[cursor[symbolAt[u]]++] = tableSize + u;
  }

  // Per-symbol encode constants.
  const deltaNbBits = new Int32Array(ALPHABET_SIZE);
  const deltaFindState = new Int32Array(ALPHABET_SIZE);
  let total = 0;
  for (let s = 0; s < ALPHABET_SIZE; s++) {
    const f = norm[s];
    if (f === 0) continue;
    const maxBitsOut = tableLog - highbit(f);
    const minStatePlus = f << maxBitsOut; // in [tableSize, 2*tableSize)
    deltaNbBits[s] = (maxBitsOut << 16) - minStatePlus;
    deltaFindState[s] = total - f;
    total += f;
  }

  // Decode table.
  const dNbBits = new Uint8Array(tableSize);
  const dNewState = new Uint16Array(tableSize);
  const symbolNext = new Uint32Array(ALPHABET_SIZE);
  for (let s = 0; s < ALPHABET_SIZE; s++) symbolNext[s] = norm[s];
  for (let u = 0; u < tableSize; u++) {
    const s = symbolAt[u];
    const next = symbolNext[s]++; // in [norm[s], 2*norm[s])
    const nb = tableLog - highbit(next);
    dNbBits[u] = nb;
    dNewState[u] = (next << nb) - tableSize;
  }

  return {
    tableLog,
    tableSize,
    norm,
    nextState,
    deltaNbBits,
    deltaFindState,
    dSymbol: symbolAt,
    dNbBits,
    dNewState,
  };
}
