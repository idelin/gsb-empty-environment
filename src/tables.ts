/**
 * CABAC probability tables, modeled after H.264/AVC (ISO/IEC 14496-10, Table 9-45/9-47).
 *
 * The probability of the Least Probable Symbol (LPS) is quantized into 64
 * states. For each state, `RANGE_TAB_LPS[state][q]` gives the LPS sub-range
 * for the 4 possible quantized values of the current interval `range`.
 */
export const NUM_STATES = 64;

export const RANGE_TAB_LPS: readonly (readonly number[])[] = [
  [128, 176, 208, 240], [128, 167, 197, 227], [128, 158, 187, 216], [123, 150, 178, 205],
  [116, 142, 169, 195], [111, 135, 160, 185], [105, 128, 152, 175], [100, 122, 144, 166],
  [95, 116, 137, 158], [90, 110, 130, 150], [85, 104, 123, 142], [81, 99, 117, 135],
  [77, 94, 111, 128], [73, 89, 105, 122], [69, 85, 100, 116], [66, 80, 95, 110],
  [62, 76, 90, 104], [59, 72, 86, 99], [56, 69, 81, 94], [53, 65, 77, 89],
  [51, 62, 73, 85], [48, 59, 69, 80], [46, 56, 66, 76], [43, 53, 63, 72],
  [41, 50, 59, 69], [39, 48, 56, 65], [37, 45, 54, 62], [35, 43, 51, 59],
  [33, 41, 48, 56], [32, 39, 46, 53], [30, 37, 43, 50], [29, 35, 41, 48],
  [27, 33, 39, 45], [26, 31, 37, 43], [24, 30, 35, 41], [23, 28, 33, 39],
  [22, 27, 32, 37], [21, 26, 30, 35], [20, 24, 29, 33], [19, 23, 27, 31],
  [18, 21, 26, 30], [17, 20, 25, 28], [16, 19, 23, 27], [15, 18, 22, 25],
  [14, 17, 21, 24], [14, 16, 20, 23], [13, 15, 19, 22], [12, 14, 18, 21],
  [12, 14, 17, 20], [11, 13, 16, 19], [11, 12, 15, 18], [10, 12, 14, 17],
  [10, 11, 13, 16], [9, 11, 13, 15], [9, 10, 12, 14], [8, 10, 12, 13],
  [8, 9, 11, 13], [7, 9, 11, 12], [7, 9, 10, 12], [7, 8, 10, 11],
  [6, 8, 9, 11], [6, 7, 9, 10], [6, 7, 8, 9], [2, 2, 2, 2],
];

/**
 * Next state index after observing an LPS. When the transition leaves state 0,
 * the MPS/LPS roles are swapped (handled by ContextModel).
 */
export const TRANS_IDX_LPS: readonly number[] = [
  0, 0, 1, 2, 2, 4, 4, 5, 6, 7, 8, 9, 9, 11, 11, 12,
  13, 13, 15, 15, 16, 16, 18, 18, 19, 19, 21, 21, 22, 22, 23, 24,
  24, 25, 26, 26, 27, 27, 28, 29, 29, 30, 30, 30, 31, 32, 32, 33,
  33, 33, 34, 34, 35, 35, 35, 36, 36, 36, 37, 37, 37, 38, 38, 63,
];

/** Approximate LPS probability of a state (used for diagnostics / static init). */
export function approxLpsProbability(stateIdx: number): number {
  return RANGE_TAB_LPS[stateIdx][0] / 256;
}

/** Pick the state whose LPS probability best matches `pLps` (0 < pLps <= 0.5). */
export function stateIdxForLpsProbability(pLps: number): number {
  let best = 0;
  let bestErr = Infinity;
  for (let s = 0; s < NUM_STATES; s++) {
    const err = Math.abs(approxLpsProbability(s) - pLps);
    if (err < bestErr) {
      bestErr = err;
      best = s;
    }
  }
  return best;
}
