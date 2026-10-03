// Shared Deflate constant tables (RFC 1951).

// Length codes 257..285: base length and extra-bit count.
export const LENGTH_BASE: readonly number[] = [
  3, 4, 5, 6, 7, 8, 9, 10,
  11, 13, 15, 17, 19, 23, 27, 31,
  35, 43, 51, 59, 67, 83, 99, 115,
  131, 163, 195, 227, 258,
];
export const LENGTH_EXTRA: readonly number[] = [
  0, 0, 0, 0, 0, 0, 0, 0,
  1, 1, 1, 1, 2, 2, 2, 2,
  3, 3, 3, 3, 4, 4, 4, 4,
  5, 5, 5, 5, 0,
];

// Distance codes 0..29: base distance and extra-bit count.
export const DIST_BASE: readonly number[] = [
  1, 2, 3, 4, 5, 7, 9, 13,
  17, 25, 33, 49, 65, 97, 129, 193,
  257, 385, 513, 769, 1025, 1537, 2049, 3073,
  4097, 6145, 8193, 12289, 16385, 24577,
];
export const DIST_EXTRA: readonly number[] = [
  0, 0, 0, 0, 1, 1, 2, 2,
  3, 3, 4, 4, 5, 5, 6, 6,
  7, 7, 8, 8, 9, 9, 10, 10,
  11, 11, 12, 12, 13, 13,
];

// Order in which code-length-code lengths are transmitted.
export const CL_ORDER: readonly number[] = [
  16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15,
];

export const NUM_LITLEN_CODES = 286;
export const NUM_DIST_CODES = 30;
export const NUM_CL_CODES = 19;
export const MAX_BITS = 15;
export const MAX_CL_BITS = 7;

// Map a match length (3..258) to its length-code index (0..28, code = 257 + index).
export function lengthToCodeIndex(length: number): number {
  for (let i = LENGTH_BASE.length - 1; i >= 0; i--) {
    if (length >= LENGTH_BASE[i]) {
      // 258 must map to code 285 (index 28), not the 227..257 bucket.
      if (i === 27 && length === 258) return 28;
      return i;
    }
  }
  throw new RangeError(`invalid match length: ${length}`);
}

// Map a distance (1..32768) to its distance code (0..29).
export function distanceToCode(distance: number): number {
  for (let i = DIST_BASE.length - 1; i >= 0; i--) {
    if (distance >= DIST_BASE[i]) return i;
  }
  throw new RangeError(`invalid distance: ${distance}`);
}

// Fixed (static) Huffman code lengths from RFC 1951 section 3.2.6.
export function fixedLitlenLengths(): number[] {
  const lengths = new Array<number>(288);
  for (let i = 0; i <= 143; i++) lengths[i] = 8;
  for (let i = 144; i <= 255; i++) lengths[i] = 9;
  for (let i = 256; i <= 279; i++) lengths[i] = 7;
  for (let i = 280; i <= 287; i++) lengths[i] = 8;
  return lengths;
}

export function fixedDistLengths(): number[] {
  return new Array<number>(32).fill(5);
}
