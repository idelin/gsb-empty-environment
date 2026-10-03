/**
 * Step 1 of the compound compressor: run-length encoding.
 *
 * Runs always alternate white/black and every row starts with a white run
 * (Fax convention), so a row beginning with black pixels starts with a
 * zero-length white run. The decoder knows the row width, so no end-of-line
 * markers are needed and rows stay strictly aligned.
 *
 * Run boundaries are located with the native TypedArray.indexOf, which
 * makes scanning long blank stretches essentially free.
 *
 * Pixel values must be exactly 0 (white) or 1 (black).
 */

export function extractRowRuns(row: Uint8Array): number[] {
  const runs: number[] = [];
  let color = 0;
  let x = 0;
  while (x < row.length) {
    let next = row.indexOf(color ^ 1, x);
    if (next === -1) next = row.length;
    runs.push(next - x);
    color ^= 1;
    x = next;
  }
  if (runs.length === 0) runs.push(0);
  return runs;
}

export function runsToRow(runs: readonly number[], width: number): Uint8Array {
  const row = new Uint8Array(width);
  let x = 0;
  let color = 0;
  for (const run of runs) {
    if (run < 0) throw new Error(`Invalid negative run length ${run}`);
    if (color === 1) row.fill(1, x, x + run);
    x += run;
    color ^= 1;
  }
  if (x !== width) {
    throw new Error(`Run lengths sum to ${x}, expected row width ${width}`);
  }
  return row;
}
