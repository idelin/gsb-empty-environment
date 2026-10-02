/** ZigZag mapping: signed integers -> unsigned integers with small magnitude. */

export function zigzagEncode32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function zigzagDecode32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}

export function zigzagEncode64(value: bigint): bigint {
  return BigInt.asUintN(64, (value << 1n) ^ (value >> 63n));
}

export function zigzagDecode64(value: bigint): bigint {
  const u = BigInt.asUintN(64, value);
  return BigInt.asIntN(64, (u >> 1n) ^ (0n - (u & 1n)));
}
