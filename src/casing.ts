/**
 * Finite case-style mapping table.
 *
 * When a rule matches case-insensitively, the case style of the matched
 * text is detected and the replacement is rewritten through this table.
 * The set of styles is finite and fixed; a missing entry means "keep the
 * replacement as written".
 */
export type CaseStyle = "lower" | "upper" | "capitalized" | "other";

export type CaseTable = Partial<Record<CaseStyle, (replacement: string) => string>>;

export const defaultCaseTable: CaseTable = {
  lower: (s) => s.toLowerCase(),
  upper: (s) => s.toUpperCase(),
  capitalized: (s) => s.charAt(0).toUpperCase() + s.slice(1).toLowerCase(),
  other: (s) => s,
};

function isCased(ch: string): boolean {
  return ch.toLowerCase() !== ch.toUpperCase();
}

export function detectCaseStyle(text: string): CaseStyle {
  const letters = [...text].filter(isCased);
  if (letters.length === 0) return "other";
  if (letters.every((c) => c.toLowerCase() === c)) return "lower";
  if (letters.every((c) => c.toUpperCase() === c)) return "upper";
  const [first, ...rest] = letters;
  if (first.toUpperCase() === first && rest.every((c) => c.toLowerCase() === c)) {
    return "capitalized";
  }
  return "other";
}

export function applyCaseTable(
  matched: string,
  replacement: string,
  table: CaseTable = defaultCaseTable,
): string {
  const style = detectCaseStyle(matched);
  const fn = table[style];
  return fn ? fn(replacement) : replacement;
}
