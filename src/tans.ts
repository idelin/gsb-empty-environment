import { BitWriter, ReverseBitReader } from "./bitstream.ts";
import { buildTables, type TansTables } from "./table.ts";
import {
  ALPHABET_SIZE,
  DEFAULT_TABLE_LOG,
  countFrequencies,
  normalizeCounts,
} from "./normalize.ts";

/**
 * Streaming tANS encoder. The entire compression state is the single integer
 * `state` in [tableSize, 2*tableSize); Push() is a constant-time table walk.
 * Symbols must be pushed in reverse order (the decoder pops them forward).
 */
export class TansEncoder {
  state: number;
  tables: TansTables;
  writer: BitWriter;

  constructor(tables: TansTables, writer: BitWriter) {
    this.tables = tables;
    this.writer = writer;
    this.state = tables.tableSize;
  }

  push(symbol: number): void {
    const t = this.tables;
    if (t.norm[symbol] === 0) throw new Error(`symbol ${symbol} not in table`);
    const nbBitsOut = (this.state + t.deltaNbBits[symbol]) >> 16;
    this.writer.writeBits(this.state & ((1 << nbBitsOut) - 1), nbBitsOut);
    this.state = t.nextState[(this.state >>> nbBitsOut) + t.deltaFindState[symbol]];
  }

  /** Flush the final state (tableLog bits) — the decoder reads it first. */
  finish(): void {
    this.writer.writeBits(this.state - this.tables.tableSize, this.tables.tableLog);
  }
}

/** Streaming tANS decoder: each Pop() is a constant-time table walk. */
export class TansDecoder {
  state: number;
  tables: TansTables;
  reader: ReverseBitReader;

  constructor(tables: TansTables, reader: ReverseBitReader) {
    this.tables = tables;
    this.reader = reader;
    this.state = reader.readBits(tables.tableLog);
  }

  pop(): number {
    const t = this.tables;
    const u = this.state;
    const symbol = t.dSymbol[u];
    this.state = t.dNewState[u] + this.reader.readBits(t.dNbBits[u]);
    return symbol;
  }
}

// ---------------------------------------------------------------------------
// Self-contained byte format:
//   'T' 'A' version(1) tableLog(1) varint(originalLength)
//   u16(numPresent) [ u8 symbol, u16 normFreq ] * numPresent
//   <bitstream bytes>
// ---------------------------------------------------------------------------

const MAGIC0 = 0x54; // 'T'
const MAGIC1 = 0x41; // 'A'
const VERSION = 1;

function writeVarint(out: number[], v: number): void {
  while (v >= 128) {
    out.push((v & 127) | 128);
    v = Math.floor(v / 128);
  }
  out.push(v);
}

function readVarint(bytes: Uint8Array, pos: number): [number, number] {
  let v = 0;
  let shift = 1;
  for (;;) {
    if (pos >= bytes.length) throw new Error("truncated header");
    const b = bytes[pos++];
    v += (b & 127) * shift;
    if (b < 128) return [v, pos];
    shift *= 128;
  }
}

export function compress(data: Uint8Array, tableLog = DEFAULT_TABLE_LOG): Uint8Array {
  const counts = countFrequencies(data);
  const norm = normalizeCounts(counts, tableLog);

  const header: number[] = [MAGIC0, MAGIC1, VERSION, tableLog];
  writeVarint(header, data.length);
  const present: number[] = [];
  for (let s = 0; s < ALPHABET_SIZE; s++) if (norm[s] > 0) present.push(s);
  header.push(present.length & 255, present.length >> 8);
  for (const s of present) {
    header.push(s, norm[s] & 255, norm[s] >> 8);
  }

  if (data.length === 0) return Uint8Array.from(header);

  const tables = buildTables(norm, tableLog);
  const writer = new BitWriter();
  const encoder = new TansEncoder(tables, writer);
  for (let i = data.length - 1; i >= 0; i--) encoder.push(data[i]);
  encoder.finish();
  const payload = writer.finish();

  const out = new Uint8Array(header.length + payload.length);
  out.set(header, 0);
  out.set(payload, header.length);
  return out;
}

export function decompress(blob: Uint8Array): Uint8Array {
  if (blob.length < 7 || blob[0] !== MAGIC0 || blob[1] !== MAGIC1) {
    throw new Error("bad magic: not a tANS stream");
  }
  if (blob[2] !== VERSION) throw new Error(`unsupported version ${blob[2]}`);
  const tableLog = blob[3];
  let pos = 4;

  const [length, afterLen] = readVarint(blob, pos);
  pos = afterLen;
  const numPresent = blob[pos] | (blob[pos + 1] << 8);
  pos += 2;
  const norm = new Uint32Array(ALPHABET_SIZE);
  for (let i = 0; i < numPresent; i++) {
    const symbol = blob[pos];
    const freq = blob[pos + 1] | (blob[pos + 2] << 8);
    pos += 3;
    norm[symbol] = freq;
  }

  if (length === 0) return new Uint8Array(0);

  const tables = buildTables(norm, tableLog);
  const reader = new ReverseBitReader(blob.subarray(pos));
  const decoder = new TansDecoder(tables, reader);
  const out = new Uint8Array(length);
  for (let i = 0; i < length; i++) out[i] = decoder.pop();
  return out;
}
