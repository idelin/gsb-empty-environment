/**
 * Finite case mapping table.
 *
 * Replacements are stored in canonical (lowercase) form. The case of the
 * matched text is classified into exactly one of the three styles below,
 * and the replacement is rendered through the corresponding table entry.
 * Anything that is not cleanly upper or capitalized falls back to "lower".
 */
export type CaseStyle = "lower" | "upper" | "capitalized";

export const CASE_TRANSFORMS: Readonly<Record<CaseStyle, (replacement: string) => string>> = {
  lower: (replacement) => replacement,
  upper: (replacement) => replacement.toUpperCase(),
  capitalized: (replacement) =>
    replacement.length === 0 ? replacement : replacement[0].toUpperCase() + replacement.slice(1),
};

export function detectCase(matched: string): CaseStyle {
  if (/[A-Za-z]/.test(matched) && matched === matched.toUpperCase()) return "upper";
  const rest = matched.slice(1);
  if (/^[A-Z]/.test(matched) && rest === rest.toLowerCase()) return "capitalized";
  return "lower";
}

export function applyCase(replacementLower: string, matched: string): string {
  return CASE_TRANSFORMS[detectCase(matched)](replacementLower);
}
