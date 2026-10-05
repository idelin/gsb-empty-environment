// Reference token expander. Deliberately independent from the compressor's
// match logic (src/lz77.ts): it takes a token list and expands it with an
// explicit byte-by-byte overlapping copy. Tests cross-check the real decoder
// against this expander.

import type { Token } from './tokens.ts';

export function expandTokens(tokens: Token[], expectedLength?: number): Uint8Array {
  const out: number[] = [];
  for (const t of tokens) {
    if (t.type === 'literal') {
      out.push(t.byte);
    } else {
      if (t.distance < 1 || t.distance > out.length) {
        throw new Error(`reference expander: illegal distance ${t.distance} at ${out.length}`);
      }
      // Overlapping copy: re-reads the output buffer as it grows, so a match
      // with length > distance replicates its own output (e.g. distance 1
      // repeats a single byte).
      for (let k = 0; k < t.length; k++) {
        out.push(out[out.length - t.distance]);
      }
    }
  }
  if (expectedLength !== undefined && out.length !== expectedLength) {
    throw new Error(`reference expander: got ${out.length} bytes, expected ${expectedLength}`);
  }
  return Uint8Array.from(out);
}
