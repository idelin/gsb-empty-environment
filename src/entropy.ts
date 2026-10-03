/** Shannon entropy of a histogram, in bits (total for the whole message). */
export function shannonEntropyBits(counts: ArrayLike<number>): number {
  let total = 0;
  for (let i = 0; i < counts.length; i++) total += counts[i];
  if (total === 0) return 0;
  let bits = 0;
  for (let i = 0; i < counts.length; i++) {
    const c = counts[i];
    if (c > 0) bits -= c * Math.log2(c / total);
  }
  return bits;
}

/** Bits a Shannon/Huffman-style integer-length code would need (sum of ceil(-log2 p)). */
export function shannonCodeLengthBits(counts: ArrayLike<number>): number {
  let total = 0;
  for (let i = 0; i < counts.length; i++) total += counts[i];
  if (total === 0) return 0;
  let bits = 0;
  for (let i = 0; i < counts.length; i++) {
    const c = counts[i];
    if (c > 0) bits += c * Math.ceil(-Math.log2(c / total));
  }
  return bits;
}
