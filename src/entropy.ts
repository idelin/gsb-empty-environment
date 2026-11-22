export function shannonEntropy(data: Uint8Array): number {
  if (data.length === 0) return 0;
  const freq = new Array<number>(256).fill(0);
  for (const byte of data) freq[byte]++;
  let entropy = 0;
  for (const count of freq) {
    if (count === 0) continue;
    const p = count / data.length;
    entropy -= p * Math.log2(p);
  }
  return entropy;
}
