/** Tab advances to the next multiple of 8; any other char advances by 1. */
export const TAB_STOP = 8;

export function visualColumn(prefix: string): number {
  let col = 0;
  for (const ch of prefix) {
    col = ch === '\t' ? col + (TAB_STOP - (col % TAB_STOP)) : col + 1;
  }
  return col;
}

/** Index of the first character where two prefixes differ (may be a.length or b.length). */
export function firstDiffIndex(a: string, b: string): number {
  let i = 0;
  while (i < a.length && i < b.length && a[i] === b[i]) i++;
  return i;
}
