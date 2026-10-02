export function zigZagEncode32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function zigZagDecode32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}

export function zigZagEncode64(value: bigint): bigint {
  return (value << 1n) ^ (value >> 63n);
}

export function zigZagDecode64(value: bigint): bigint {
  return (value >> 1n) ^ -(value & 1n);
}
