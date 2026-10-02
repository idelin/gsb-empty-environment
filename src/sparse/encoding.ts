/**
 * 压缩原语：varint、delta 编码、RLE。
 * 针对海量稀疏整数下标 + 重复值的数据分布做极致空间压缩。
 */

/** 无符号 varint（支持到 2^53-1 的安全整数）。 */
export function writeVarint(out: number[], value: number): void {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new RangeError(`varint requires non-negative safe integer, got ${value}`);
  }
  let v = value;
  while (v >= 0x80) {
    out.push((v & 0x7f) | 0x80);
    v = Math.floor(v / 128);
  }
  out.push(v);
}

export function readVarint(buf: Uint8Array, pos: { p: number }): number {
  let result = 0;
  let shift = 1; // 乘数（避免位移溢出，支持 2^53）
  for (;;) {
    if (pos.p >= buf.length) throw new RangeError("varint: unexpected end of buffer");
    const b = buf[pos.p++];
    result += (b & 0x7f) * shift;
    if ((b & 0x80) === 0) break;
    shift *= 128;
    if (shift > 2 ** 56) throw new RangeError("varint: too long");
  }
  if (!Number.isSafeInteger(result)) throw new RangeError("varint: overflow");
  return result;
}

/** 对严格递增下标序列做 delta + varint 编码。 */
export function writeDeltaIndices(out: number[], sortedIndices: ArrayLike<number>): void {
  writeVarint(out, sortedIndices.length);
  let prev = -1;
  for (let i = 0; i < sortedIndices.length; i++) {
    const idx = sortedIndices[i];
    writeVarint(out, idx - prev - 1); // gap >= 0
    prev = idx;
  }
}

export function readDeltaIndices(buf: Uint8Array, pos: { p: number }): number[] {
  const count = readVarint(buf, pos);
  const out = new Array<number>(count);
  let prev = -1;
  for (let i = 0; i < count; i++) {
    prev = prev + 1 + readVarint(buf, pos);
    out[i] = prev;
  }
  return out;
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function writeString(out: number[], s: string): void {
  const bytes = encoder.encode(s);
  writeVarint(out, bytes.length);
  for (const b of bytes) out.push(b);
}

export function readString(buf: Uint8Array, pos: { p: number }): string {
  const len = readVarint(buf, pos);
  if (pos.p + len > buf.length) throw new RangeError("string: unexpected end of buffer");
  const s = decoder.decode(buf.subarray(pos.p, pos.p + len));
  pos.p += len;
  return s;
}

/**
 * 值序列压缩：字典编码 + RLE。
 * 先对去重值建字典（JSON 序列化为键），再对字典 id 序列做 RLE。
 * 对“少量重复值 + 大行程”与“值重复但行程短”两种分布均最优。
 */
export function writeRleValues(out: number[], values: ArrayLike<unknown>): void {
  writeVarint(out, values.length);
  // 字典
  const dict = new Map<string, number>();
  const dictValues: string[] = [];
  const ids = new Array<number>(values.length);
  for (let i = 0; i < values.length; i++) {
    const json = JSON.stringify(values[i]) ?? "null";
    let id = dict.get(json);
    if (id === undefined) {
      id = dictValues.length;
      dict.set(json, id);
      dictValues.push(json);
    }
    ids[i] = id;
  }
  writeVarint(out, dictValues.length);
  for (const json of dictValues) writeString(out, json);
  // 字典 id 序列 RLE
  let i = 0;
  while (i < ids.length) {
    let run = 1;
    while (i + run < ids.length && ids[i + run] === ids[i]) run++;
    writeVarint(out, run);
    writeVarint(out, ids[i]);
    i += run;
  }
}

export function readRleValues(buf: Uint8Array, pos: { p: number }): unknown[] {
  const count = readVarint(buf, pos);
  const dictSize = readVarint(buf, pos);
  const dict = new Array<unknown>(dictSize);
  for (let d = 0; d < dictSize; d++) dict[d] = JSON.parse(readString(buf, pos));
  const out = new Array<unknown>(count);
  let i = 0;
  while (i < count) {
    const run = readVarint(buf, pos);
    const value = dict[readVarint(buf, pos)];
    for (let r = 0; r < run; r++) out[i++] = value;
  }
  return out;
}
