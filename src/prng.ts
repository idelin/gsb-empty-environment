/**
 * Deterministic PRNG (splitmix32). Used to derive the Buzhash byte table and
 * reproducible test fixtures, so every run is verifiable without external state.
 */
export function splitmix32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x9e3779b9) >>> 0;
    let z = state;
    z = Math.imul(z ^ (z >>> 16), 0x21f0aaad);
    z = Math.imul(z ^ (z >>> 15), 0x735a2d97);
    z = (z ^ (z >>> 15)) >>> 0;
    return z;
  };
}

/** Fill a buffer with deterministic pseudo-random bytes from the given seed. */
export function randomBytes(length: number, seed: number): Buffer {
  const next = splitmix32(seed);
  const buf = Buffer.allocUnsafe(length);
  for (let i = 0; i < length; i += 4) {
    const word = next();
    buf[i] = word & 0xff;
    if (i + 1 < length) buf[i + 1] = (word >>> 8) & 0xff;
    if (i + 2 < length) buf[i + 2] = (word >>> 16) & 0xff;
    if (i + 3 < length) buf[i + 3] = (word >>> 24) & 0xff;
  }
  return buf;
}
