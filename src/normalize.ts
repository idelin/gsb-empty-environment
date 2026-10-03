/** Symbol alphabet: single bytes. */
export const ALPHABET_SIZE = 256;
export const DEFAULT_TABLE_LOG = 12; // 4096 states
export const MAX_TABLE_LOG = 15; // Uint16 tables => tableSize <= 32768

/** floor(log2(v)) for v >= 1. */
export function highbit(v: number): number {
  return 31 - Math.clz32(v);
}

export function countFrequencies(data: Uint8Array): Uint32Array {
  const counts = new Uint32Array(ALPHABET_SIZE);
  for (let i = 0; i < data.length; i++) counts[data[i]]++;
  return counts;
}

/**
 * Normalize a histogram so the frequencies sum exactly to `1 << tableLog`,
 * keeping every present symbol at frequency >= 1 (largest-remainder method
 * with fix-up passes).
 */
export function normalizeCounts(counts: Uint32Array, tableLog: number): Uint32Array {
  if (tableLog < 1 || tableLog > MAX_TABLE_LOG) {
    throw new Error(`tableLog must be in [1, ${MAX_TABLE_LOG}], got ${tableLog}`);
  }
  const tableSize = 1 << tableLog;
  const norm = new Uint32Array(ALPHABET_SIZE);

  let total = 0;
  let present = 0;
  let maxSym = 0;
  for (let s = 0; s < ALPHABET_SIZE; s++) {
    total += counts[s];
    if (counts[s] > 0) {
      present++;
      if (counts[s] >= counts[maxSym]) maxSym = s;
    }
  }
  if (total === 0) return norm;
  if (present > tableSize) {
    throw new Error(`table too small: ${present} distinct symbols > ${tableSize} states`);
  }
  if (present === 1) {
    norm[maxSym] = tableSize;
    return norm;
  }

  // Largest remainder: floor, clamp to >= 1, then fix the residual.
  let sum = 0;
  for (let s = 0; s < ALPHABET_SIZE; s++) {
    if (counts[s] === 0) continue;
    const exact = (counts[s] * tableSize) / total;
    norm[s] = Math.max(1, Math.floor(exact));
    sum += norm[s];
  }

  while (sum > tableSize) {
    // Shrink the symbol whose removal hurts least: largest norm > 1.
    let best = -1;
    for (let s = 0; s < ALPHABET_SIZE; s++) {
      if (norm[s] > 1 && (best < 0 || norm[s] > norm[best])) best = s;
    }
    norm[best]--;
    sum--;
  }
  while (sum < tableSize) {
    // Grow the symbol with the largest fractional deficit.
    let best = -1;
    let bestDeficit = -1;
    for (let s = 0; s < ALPHABET_SIZE; s++) {
      if (counts[s] === 0) continue;
      const deficit = (counts[s] * tableSize) / total - norm[s];
      if (deficit > bestDeficit) {
        bestDeficit = deficit;
        best = s;
      }
    }
    norm[best]++;
    sum++;
  }
  return norm;
}
