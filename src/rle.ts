/**
 * Self-built run-length encoding for byte arrays (PackBits-style pairs
 * [count, value]). Used to compress stored history deltas in memory.
 */
export function rleEncode(src: Uint8Array): Uint8Array {
  const out: number[] = [];
  let i = 0;
  while (i < src.length) {
    let run = 1;
    while (i + run < src.length && src[i + run] === src[i] && run < 255) run++;
    out.push(run, src[i]);
    i += run;
  }
  return Uint8Array.from(out);
}

export function rleDecode(data: Uint8Array): Uint8Array {
  if (data.length % 2 !== 0) throw new Error('corrupt rle stream');
  let len = 0;
  for (let i = 0; i < data.length; i += 2) len += data[i];
  const out = new Uint8Array(len);
  let o = 0;
  for (let i = 0; i < data.length; i += 2) {
    out.fill(data[i + 1], o, o + data[i]);
    o += data[i];
  }
  return out;
}

/** A byte payload kept either raw or RLE-compressed, whichever is smaller. */
export interface ByteBlock {
  data: Uint8Array;
  rle: boolean;
}

export function packBlock(raw: Uint8Array, compress: boolean): ByteBlock {
  if (!compress || raw.length < 4) return { data: raw, rle: false };
  const enc = rleEncode(raw);
  return enc.length < raw.length ? { data: enc, rle: true } : { data: raw, rle: false };
}

export function expandBlock(block: ByteBlock): Uint8Array {
  return block.rle ? rleDecode(block.data) : block.data;
}
