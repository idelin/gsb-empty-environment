// LZ77 token model shared by the compressor, the block codec and the
// reference expander.

export type Token =
  | { type: 'literal'; byte: number }
  | { type: 'match'; length: number; distance: number };

// Length slot: length L (2..511) maps to slot floor(log2(L)); the slot covers
// lengths [2^slot, 2^(slot+1)-1] with `slot` extra bits. Slots 1..8 map to
// main-alphabet symbols 256..263.
export function lengthSlot(length: number): number {
  return 31 - Math.clz32(length);
}

export function lengthSymbol(slot: number): number {
  return 256 + slot - 1;
}

export function symbolToLengthSlot(symbol: number): number {
  return symbol - 256 + 1;
}

// Distance slot: distance d >= 1 maps to slot floor(log2(d)); the slot covers
// distances [2^slot, 2^(slot+1)-1] with `slot` extra bits.
export function distanceSlot(distance: number): number {
  return 31 - Math.clz32(distance);
}
