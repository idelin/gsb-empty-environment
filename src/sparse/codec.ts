/**
 * 压缩编解码器：
 *  - varint（LEB128，支持到 2^53 的安全整数）
 *  - zigzag 有符号映射
 *  - 有序下标的 delta 编码
 *  - 等值游程编码（RLE）
 * 全部确定性、可往返（round-trip）。
 */

export function encodeVarint(n: number, out: number[]): void {
  if (!Number.isSafeInteger(n) || n < 0) throw new RangeError(`varint requires non-negative safe integer, got ${n}`);
  while (n >= 0x80) {
    out.push((n % 0x80) | 0x80);
    n = Math.floor(n / 0x80);
  }
  out.push(n);
}

export function decodeVarint(buf: Uint8Array, pos: { p: number }): number {
  let result = 0;
  let shift = 1; // 乘数（避免位移溢出 32 位）
  for (;;) {
    if (pos.p >= buf.length) throw new RangeError("varint: buffer underflow");
    const b = buf[pos.p++];
    result += (b & 0x7f) * shift;
    if ((b & 0x80) === 0) break;
    shift *= 0x80;
    if (shift > Number.MAX_SAFE_INTEGER) throw new RangeError("varint: overflow");
  }
  return result;
}

export function zigzagEncode(n: number): number {
  if (!Number.isSafeInteger(n)) throw new RangeError(`zigzag requires safe integer, got ${n}`);
  return n >= 0 ? n * 2 : -n * 2 - 1;
}

export function zigzagDecode(z: number): number {
  return z % 2 === 0 ? z / 2 : -(z + 1) / 2;
}

/** 有序下标 -> delta+varint 字节流。 */
export function encodeDeltaIndices(sortedIndices: number[]): Uint8Array {
  const out: number[] = [];
  let prev = 0;
  for (const idx of sortedIndices) {
    encodeVarint(idx - prev, out);
    prev = idx;
  }
  return Uint8Array.from(out);
}

export function decodeDeltaIndices(buf: Uint8Array, count: number): number[] {
  const pos = { p: 0 };
  const out: number[] = [];
  let prev = 0;
  for (let i = 0; i < count; i++) {
    prev += decodeVarint(buf, pos);
    out.push(prev);
  }
  return out;
}

/**
 * 值序列 -> RLE(zigzag varint)。格式：重复 (runLength, zigzag(value))。
 * 游程长度 >= 2 时才有收益，解码端按原样还原。
 */
export function encodeRleValues(values: number[]): Uint8Array {
  const out: number[] = [];
  let i = 0;
  while (i < values.length) {
    let run = 1;
    while (i + run < values.length && values[i + run] === values[i] && run < Number.MAX_SAFE_INTEGER / 2) {
      run++;
    }
    encodeVarint(run, out);
    encodeVarint(zigzagEncode(values[i]), out);
    i += run;
  }
  return Uint8Array.from(out);
}

export function decodeRleValues(buf: Uint8Array, count: number): number[] {
  const pos = { p: 0 };
  const out: number[] = [];
  while (out.length < count) {
    const run = decodeVarint(buf, pos);
    const value = zigzagDecode(decodeVarint(buf, pos));
    for (let i = 0; i < run; i++) out.push(value);
  }
  if (out.length !== count) throw new RangeError("rle: length mismatch");
  return out;
}
