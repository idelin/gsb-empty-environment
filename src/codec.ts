export function writeVarint(out: number[], value: number): void {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new RangeError(`varint requires a non-negative safe integer, got ${value}`);
  }
  let v = value;
  while (v >= 0x80) {
    out.push((v % 0x80) | 0x80);
    v = Math.floor(v / 0x80);
  }
  out.push(v);
}

export function readVarint(buf: Uint8Array, state: { offset: number }): number {
  let result = 0;
  let multiplier = 1;
  for (;;) {
    if (state.offset >= buf.length) throw new Error('truncated varint');
    const byte = buf[state.offset++];
    result += (byte & 0x7f) * multiplier;
    if (byte < 0x80) return result;
    multiplier *= 0x80;
    if (multiplier > Number.MAX_SAFE_INTEGER) throw new Error('varint exceeds safe integer range');
  }
}

export function zigzagEncode(value: number): number {
  if (!Number.isSafeInteger(value)) throw new RangeError(`zigzag requires a safe integer, got ${value}`);
  if (value > MAX_ZIGZAG_VALUE || value < -MAX_ZIGZAG_VALUE) {
    throw new RangeError(`zigzag supports |value| <= ${MAX_ZIGZAG_VALUE}, got ${value}`);
  }
  return value >= 0 ? value * 2 : -value * 2 - 1;
}

export const MAX_ZIGZAG_VALUE = Math.floor(Number.MAX_SAFE_INTEGER / 2);

export function zigzagDecode(z: number): number {
  return z % 2 === 0 ? z / 2 : -(z + 1) / 2;
}

export function encodeSparseEntries(entries: Array<readonly [number, number]>): Uint8Array {
  const out: number[] = [];
  writeVarint(out, entries.length);
  let prev = -1;
  let i = 0;
  while (i < entries.length) {
    const index = entries[i][0];
    if (index <= prev) throw new Error('entries must be sorted by strictly increasing index');
    let run = 0;
    while (i + run < entries.length && entries[i + run][0] === index + run) run++;
    if (run >= 3) {
      writeVarint(out, 0);
      writeVarint(out, run);
      prev = entries[i + run - 1][0];
      i += run;
    } else {
      writeVarint(out, index - prev);
      prev = index;
      i++;
    }
  }
  i = 0;
  while (i < entries.length) {
    let j = i + 1;
    while (j < entries.length && entries[j][1] === entries[i][1]) j++;
    writeVarint(out, j - i);
    writeVarint(out, zigzagEncode(entries[i][1]));
    i = j;
  }
  return Uint8Array.from(out);
}

export function decodeSparseEntries(buf: Uint8Array): Array<[number, number]> {
  const state = { offset: 0 };
  const count = readVarint(buf, state);
  const indices = new Array<number>(count);
  let prev = -1;
  let produced = 0;
  while (produced < count) {
    const d = readVarint(buf, state);
    if (d === 0) {
      const run = readVarint(buf, state);
      if (run < 3 || produced + run > count) throw new Error('corrupt index run block');
      for (let r = 0; r < run; r++) indices[produced++] = ++prev;
    } else {
      prev += d;
      indices[produced++] = prev;
    }
  }
  const result: Array<[number, number]> = [];
  let k = 0;
  while (k < count) {
    const run = readVarint(buf, state);
    const value = zigzagDecode(readVarint(buf, state));
    if (run <= 0 || k + run > count) throw new Error('corrupt RLE block');
    for (let r = 0; r < run; r++) result.push([indices[k++], value]);
  }
  if (state.offset !== buf.length) throw new Error('trailing bytes after sparse payload');
  return result;
}
