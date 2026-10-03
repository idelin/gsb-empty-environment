/**
 * tANS finite-state entropy coder.
 *
 * The entire compression state is one integer `state` in [M, 2M). Push/Pop
 * are O(1) table lookups plus a single variable-length bit flush per symbol.
 *
 * ANS is a stack: symbols are decoded in reverse order of encoding.
 */
import { BitReader, BitWriter } from "./bitstream.ts";
import type { TansTable } from "./table.ts";

export class TansEncoder {
  private state: number;
  private readonly table: TansTable;

  constructor(table: TansTable) {
    this.table = table;
    this.state = table.size;
  }

  /** Encode one symbol, flushing renormalisation bits to `out`. */
  push(symbol: number, out: BitWriter): void {
    const e = this.table.encode[symbol];
    if (!e) throw new Error(`symbol ${symbol} not in table`);
    const x = this.state;
    // Flush nb bits so that y = x >> nb lands in [freq, 2*freq).
    const nb = x < e.threshold ? e.shift - 1 : e.shift;
    if (nb > 0) out.write(x & ((1 << nb) - 1), nb);
    this.state = e.slots[(x >> nb) - e.freq];
  }

  /** Current raw state (always in [M, 2M)); exposed for testing. */
  get currentState(): number {
    return this.state;
  }

  /** Flush the final state (tableLog bits) and return the packed stream. */
  finish(out: BitWriter): { bytes: Uint8Array; bitLength: number } {
    out.write(this.state - this.table.size, this.table.tableLog);
    return out.finish();
  }
}

export class TansDecoder {
  private state: number;
  private readonly table: TansTable;
  private readonly input: BitReader;

  constructor(table: TansTable, input: BitReader) {
    this.table = table;
    this.input = input;
    this.state = table.size + input.read(table.tableLog);
  }

  /** Decode one symbol in O(1). */
  pop(): number {
    const d = this.table.decode[this.state - this.table.size];
    this.state = d.nextState + (d.numBits > 0 ? this.input.read(d.numBits) : 0);
    return d.symbol;
  }
}

/** Convenience: encode a whole buffer (reverse-encode so decode pops forward). */
export function encodeAll(
  table: TansTable,
  data: Uint8Array,
): { bytes: Uint8Array; bitLength: number } {
  const out = new BitWriter();
  const enc = new TansEncoder(table);
  for (let i = data.length - 1; i >= 0; i--) enc.push(data[i], out);
  return enc.finish(out);
}

/** Convenience: decode `count` symbols. */
export function decodeAll(
  table: TansTable,
  bytes: Uint8Array,
  bitLength: number,
  count: number,
): Uint8Array {
  const dec = new TansDecoder(table, new BitReader(bytes, bitLength));
  const out = new Uint8Array(count);
  for (let i = 0; i < count; i++) out[i] = dec.pop();
  return out;
}
