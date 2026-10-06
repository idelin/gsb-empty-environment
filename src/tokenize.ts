const TOKEN_PATTERN = /[\p{L}\p{N}]+/gu;

/**
 * Splits a raw query string into normalized tokens.
 * Lowercases, splits on any non-letter/non-digit run (whitespace,
 * punctuation), drops empties, and dedupes keeping first position.
 */
export function tokenize(raw: string): string[] {
  const seen = new Set<string>();
  const tokens: string[] = [];
  for (const match of raw.toLowerCase().matchAll(TOKEN_PATTERN)) {
    const token = match[0];
    if (!seen.has(token)) {
      seen.add(token);
      tokens.push(token);
    }
  }
  return tokens;
}
