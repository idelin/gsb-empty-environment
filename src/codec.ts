/**
 * Self-describing compressed container:
 *
 *   magic "tANS" (4B) | tableLog (1B) | symbolCount (2B LE)
 *   | originalLength (4B LE) | payloadBitLength (4B LE)
 *   | [symbol (2B LE), freq (4B LE)] * symbolCount
 *   | payload bytes
 */
import { BitReader, BitWriter } from "./bitstream.ts";
import { TansDecoder, TansEncoder } from "./tans.ts";
import { buildTable, countSymbols, normalizeFrequencies, type TansTable } from "./table.ts";

const MAGIC = 0x534e4154; // "tANS" little-endian

export interface CompressResult {
  bytes: Uint8Array;
  originalBytes: number;
  compressedBytes: number;
  ratio: number;
  entropyBitsPerSymbol: number;
  achievedBitsPerSymbol: number;
  table: TansTable;
}

export function compress(data: Uint8Array, tableLog = 12): CompressResult {
  if (data.length === 0) throw new Error("cannot compress empty input");
  const counts = countSymbols(data);
  const freq = normalizeFrequencies(counts, tableLog);
  const table = buildTable(freq, tableLog);

  const out = new BitWriter();
  const enc = new TansEncoder(table);
  for (let i = data.length - 1; i >= 0; i--) enc.push(data[i], out);
  const payload = enc.finish(out);

  const symbols = table.symbols;
  const headerBytes = 4 + 1 + 2 + 4 + 4 + symbols.length * 6;
  // payload.bytes is word-aligned; a few inert padding bytes may trail.
  const bytes = new Uint8Array(headerBytes + payload.bytes.length);
  const view = new DataView(bytes.buffer);
  let p = 0;
  view.setUint32(p, MAGIC, true); p += 4;
  view.setUint8(p, tableLog); p += 1;
  view.setUint16(p, symbols.length, true); p += 2;
  view.setUint32(p, data.length, true); p += 4;
  view.setUint32(p, payload.bitLength, true); p += 4;
  for (const s of symbols) {
    view.setUint16(p, s, true); p += 2;
    view.setUint32(p, table.freq[s], true); p += 4;
  }
  bytes.set(payload.bytes, p);

  const entropy = shannonEntropy(data);
  return {
    bytes,
    originalBytes: data.length,
    compressedBytes: bytes.length,
    ratio: data.length / bytes.length,
    entropyBitsPerSymbol: entropy,
    achievedBitsPerSymbol: (bytes.length * 8) / data.length,
    table,
  };
}

export function decompress(blob: Uint8Array): Uint8Array {
  const view = new DataView(blob.buffer, blob.byteOffset, blob.byteLength);
  let p = 0;
  if (view.getUint32(p, true) !== MAGIC) throw new Error("bad magic");
  p += 4;
  const tableLog = view.getUint8(p); p += 1;
  const nsyms = view.getUint16(p, true); p += 2;
  const originalLength = view.getUint32(p, true); p += 4;
  const payloadBits = view.getUint32(p, true); p += 4;

  let maxSym = 0;
  const pairs: [number, number][] = [];
  for (let i = 0; i < nsyms; i++) {
    const s = view.getUint16(p, true); p += 2;
    const f = view.getUint32(p, true); p += 4;
    pairs.push([s, f]);
    if (s > maxSym) maxSym = s;
  }
  const freq = new Uint32Array(maxSym + 1);
  for (const [s, f] of pairs) freq[s] = f;
  const table = buildTable(freq, tableLog);

  const payload = blob.subarray(p);
  const dec = new TansDecoder(table, new BitReader(payload, payloadBits));
  const out = new Uint8Array(originalLength);
  for (let i = 0; i < originalLength; i++) out[i] = dec.pop();
  return out;
}

/** Shannon entropy of the empirical distribution, bits per symbol. */
export function shannonEntropy(data: Uint8Array): number {
  const counts = countSymbols(data);
  const n = data.length;
  let h = 0;
  for (const c of counts.values()) {
    const p = c / n;
    h -= p * Math.log2(p);
  }
  return h;
}
