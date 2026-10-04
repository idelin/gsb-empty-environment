/**
 * Versioned abbreviation dictionary.
 *
 * Entries include their trailing period, e.g. "Dr.", "U.S.A.".
 * Besides exact lookup, the dictionary tracks every proper prefix of every
 * entry so the scanner can tell that a token like "U." might still grow into
 * "U.S." or "U.S.A." when more input arrives.
 */
export class AbbreviationDictionary {
  private readonly entries: Set<string>;
  private readonly prefixes: Set<string>;

  constructor(entries: Iterable<string>) {
    this.entries = new Set(entries);
    this.prefixes = new Set();
    for (const entry of this.entries) {
      for (let i = 1; i < entry.length; i++) {
        this.prefixes.add(entry.slice(0, i));
      }
    }
  }

  /** True when `token` (including its trailing period) is a known abbreviation. */
  has(token: string): boolean {
    return this.entries.has(token);
  }

  /** True when `token` is a proper prefix of some entry (prefix overlap). */
  hasPrefix(token: string): boolean {
    return this.prefixes.has(token);
  }
}
