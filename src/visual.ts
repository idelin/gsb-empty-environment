/** Visual-column math for a fixed tab width. Columns are 0-based. */

export const TAB_WIDTH = 4;

/**
 * Visual column of the character at index `upto` within `line`
 * (a single line, no "\n"). A "\t" advances to the next multiple
 * of `tabWidth`; every other code unit advances by 1.
 */
export function visualColumn(line: string, upto: number = line.length, tabWidth: number = TAB_WIDTH): number {
  let col = 0;
  const n = Math.min(upto, line.length);
  for (let i = 0; i < n; i++) {
    col = line.charCodeAt(i) === 9 ? nextTabStop(col, tabWidth) : col + 1;
  }
  return col;
}

/** Smallest tab stop strictly greater than `col`. */
export function nextTabStop(col: number, tabWidth: number = TAB_WIDTH): number {
  return col + (tabWidth - (col % tabWidth));
}

/** Largest tab stop strictly less than `col` (0 when `col` is 0). */
export function prevTabStop(col: number, tabWidth: number = TAB_WIDTH): number {
  return col === 0 ? 0 : Math.floor((col - 1) / tabWidth) * tabWidth;
}

/** Whitespace string that advances column `col` to the next tab stop. */
export function tabInsertion(col: number, useTabs: boolean, tabWidth: number = TAB_WIDTH): string {
  if (useTabs) return "\t";
  return " ".repeat(nextTabStop(col, tabWidth) - col);
}
