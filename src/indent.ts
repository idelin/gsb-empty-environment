/** Tab stops are every 8 columns. */
export const TAB_STOP = 8;

/**
 * Visual column of an indentation prefix (0-based width).
 * A tab advances to the next multiple of `tabStop`; every other
 * character (in practice only spaces are valid) advances by one.
 */
export function visualColumn(prefix: string, tabStop: number = TAB_STOP): number {
  let col = 0;
  for (const ch of prefix) {
    col = ch === "\t" ? col + tabStop - (col % tabStop) : col + 1;
  }
  return col;
}
