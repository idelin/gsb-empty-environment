import { WireError } from './errors.ts';

export const MAX_VARINT_BYTES = 10;

export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt.asUintN(64, BigInt(value));
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

export function decodeVarint(
  buf: Uint8Array,
  offset: number,
  limit: number,
): { value: bigint; next: number } {
  let result = 0n;
  let shift = 0n;
  let pos = offset;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    if (pos >= limit) {
      throw new WireError(
        'varint 截断：第 ' + (i + 1) + ' 个字节缺失，安全越界防护已触发',
        pos,
      );
    }
    const byte = buf[pos];
    pos++;
    if (i === MAX_VARINT_BYTES - 1 && byte > 1) {
      throw new WireError('varint 溢出：超过 64 位可表示范围', offset);
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result, next: pos };
    }
    shift += 7n;
  }
  throw new WireError('varint 溢出：超过 10 字节上限', offset);
}

export function zigzagEncode(value: bigint | number): bigint {
  const n = BigInt(value);
  return BigInt.asUintN(64, (n << 1n) ^ (n >> 63n));
}

export function zigzagDecode(value: bigint): bigint {
  return BigInt.asIntN(64, (value >> 1n) ^ (-(value & 1n)));
}
