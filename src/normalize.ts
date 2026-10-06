import type { NormalizeFn } from './types.ts';

/**
 * Default key normalization: Unicode NFKC, trim, case-fold and collapse
 * internal whitespace, so `"  Hello\tWorld "` and `"hello world"` dedupe
 * to the same suggestion.
 */
export const defaultNormalize: NormalizeFn = (key) =>
  key.normalize('NFKC').trim().toLowerCase().replace(/\s+/g, ' ');
