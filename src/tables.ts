/**
 * RFC 1951 section 3.2.5 constant tables: length/distance code bases and
 * extra-bit counts, plus the code-length-code transmission order.
 */

/** Base match lengths for length codes 257..285 (index 0..28). */
export const LENGTH_BASE: readonly number[] = [
  3, 4, 5, 6, 7, 8, 9, 10,
  11, 13, 15, 17, 19, 23, 27, 31,
  35, 43, 51, 59, 67, 83, 99, 115,
  131, 163, 195, 227, 258,
];

/** Extra bits for length codes 257..285. */
export const LENGTH_EXTRA: readonly number[] = [
  0, 0, 0, 0, 0, 0, 0, 0,
  1, 1, 1, 1, 2, 2, 2, 2,
  3, 3, 3, 3, 4, 4, 4, 4,
  5, 5, 5, 5, 0,
];

/** Base distances for distance codes 0..29. */
export const DIST_BASE: readonly number[] = [
  1, 2, 3, 4, 5, 7, 9, 13,
  17, 25, 33, 49, 65, 97, 129, 193,
  257, 385, 513, 769, 1025, 1537, 2049, 3073,
  4097, 6145, 8193, 12289, 16385, 24577,
];

/** Extra bits for distance codes 0..29. */
export const DIST_EXTRA: readonly number[] = [
  0, 0, 0, 0, 1, 1, 2, 2,
  3, 3, 4, 4, 5, 5, 6, 6,
  7, 7, 8, 8, 9, 9, 10, 10,
  11, 11, 12, 12, 13, 13,
];

/** Order in which code-length-code lengths are transmitted (RFC 1951 3.2.7). */
export const CLCODE_ORDER: readonly number[] = [
  16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15,
];

export const NUM_LITLEN_CODES = 286;
export const NUM_DIST_CODES = 30;
export const NUM_CL_CODES = 19;
export const MAX_BITS = 15;
export const MAX_CL_BITS = 7;
export const END_OF_BLOCK = 256;

export const MIN_MATCH = 3;
export const MAX_MATCH = 258;
export const WINDOW_SIZE = 32768;

/** length (3..258) -> length code (257..285). Length 258 maps to 285. */
const lengthToCode = new Uint16Array(MAX_MATCH + 1);
for (let code = 0; code < LENGTH_BASE.length - 1; code++) {
  const lo = LENGTH_BASE[code];
  const hi = LENGTH_BASE[code + 1];
  for (let len = lo; len < hi; len++) lengthToCode[len] = 257 + code;
}
lengthToCode[258] = 285;

export function lengthCodeOf(length: number): number {
  return lengthToCode[length];
}

/** distance (1..32768) -> distance code (0..29). */
export function distCodeOf(distance: number): number {
  let lo = 0;
  let hi = DIST_BASE.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (DIST_BASE[mid] <= distance) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}
