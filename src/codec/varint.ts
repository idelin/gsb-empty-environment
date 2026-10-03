import { CodecError } from './errors.ts';

/** Maximum bytes of a LEB128 varint we accept (64-bit). */
export const MAX_VARINT_BYTES = 10;

export function assertVarintValue(value: number): void {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new CodecError('VARINT_OVERFLOW', `varint requires a non-negative safe integer, got ${value}`);
  }
}

export function varintLength(value: number): number {
  assertVarintValue(value);
  let n = 1;
  let v = Math.floor(value / 128);
  while (v > 0) {
    n++;
    v = Math.floor(v / 128);
  }
  return n;
}

export function writeVarint(writeByte: (b: number) => void, value: number): void {
  assertVarintValue(value);
  let v = value;
  while (v >= 128) {
    const low = v % 128;
    writeByte(low | 0x80);
    v = (v - low) / 128;
  }
  writeByte(v);
}

export function readVarint(readByte: () => number): number {
  let result = 0;
  let shift = 0;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const b = readByte();
    result += (b & 0x7f) * 2 ** shift;
    if ((b & 0x80) === 0) {
      if (!Number.isSafeInteger(result)) {
        throw new CodecError('VARINT_OVERFLOW', `varint overflows safe integer range: ${result}`);
      }
      return result;
    }
    shift += 7;
  }
  throw new CodecError('VARINT_OVERFLOW', `varint exceeds ${MAX_VARINT_BYTES} bytes`);
}
