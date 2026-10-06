/**
 * Unicode code-point helpers.
 *
 * All offsets inside the editor state machine are expressed in code points
 * (not UTF-16 code units, not grapheme clusters). Iterating a string with
 * `for...of` / spread yields code points, so `[...text]` splits correctly
 * on surrogate-pair boundaries.
 */

export function codePointLength(text: string): number {
  let count = 0;
  for (const _char of text) count += 1;
  return count;
}

export function codePointSlice(text: string, start: number, end?: number): string {
  return [...text].slice(start, end).join('');
}

/**
 * Replace `deleteCount` code points starting at code-point offset `start`
 * with `insert`.
 */
export function spliceText(
  text: string,
  start: number,
  deleteCount: number,
  insert: string,
): string {
  return codePointSlice(text, 0, start) + insert + codePointSlice(text, start + deleteCount);
}
