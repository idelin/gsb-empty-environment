/**
 * Splits a raw query string into search terms.
 *
 * - Terms are separated by runs of whitespace.
 * - A double-quoted span ("...") is kept as a single term, quotes stripped.
 * - An unterminated quote consumes the rest of the input as one term.
 * - Empty / whitespace-only input yields an empty token list.
 */
export function tokenizeQuery(raw: string): string[] {
  const tokens: string[] = [];
  let current = "";
  let inQuotes = false;
  let hasCurrent = false;

  const flush = (): void => {
    if (hasCurrent) {
      tokens.push(current);
      current = "";
      hasCurrent = false;
    }
  };

  for (const ch of raw) {
    if (ch === '"') {
      if (inQuotes) {
        flush();
        inQuotes = false;
      } else {
        flush();
        inQuotes = true;
        hasCurrent = true;
      }
    } else if (!inQuotes && /\s/.test(ch)) {
      flush();
    } else {
      current += ch;
      hasCurrent = true;
    }
  }
  flush();
  return tokens;
}
