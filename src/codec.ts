import { BitWriter, BitReader } from "./bitstream.js";
import { FrequencyModel } from "./model.js";
import { ArithmeticEncoder, ArithmeticDecoder } from "./arithmetic.js";

// Self-contained stream format:
//   varint  symbolCount          (number of decoded symbols -> exact stop)
//   varint  alphabetSize
//   alphabetSize x { byte symbol, varint frequency }
//   ...arithmetic-coded bitstream (MSB-first, zero-padded)

function writeVarint(out: number[], value: number): void {
  let v: number = value;
  while (v >= 0x80) {
    out.push((v % 128) | 0x80);
    v = Math.floor(v / 128);
  }
  out.push(v);
}

class ByteCursor {
  private data: Uint8Array;
  pos: number = 0;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  readByte(): number {
    if (this.pos >= this.data.length) {
      throw new Error("unexpected end of stream header");
    }
    const b: number = this.data[this.pos];
    this.pos += 1;
    return b;
  }

  readVarint(): number {
    let shift: number = 1;
    let value: number = 0;
    for (;;) {
      const b: number = this.readByte();
      value += (b % 128) * shift;
      if (b < 0x80) {
        return value;
      }
      shift *= 128;
    }
  }
}

export function compress(data: Uint8Array, model: FrequencyModel): Uint8Array {
  const header: number[] = [];
  writeVarint(header, data.length);
  writeVarint(header, model.symbols.length);
  for (let i: number = 0; i < model.symbols.length; i++) {
    header.push(model.symbols[i]);
    writeVarint(header, model.cum[i + 1] - model.cum[i]);
  }

  const writer: BitWriter = new BitWriter();
  const encoder: ArithmeticEncoder = new ArithmeticEncoder(writer);
  for (let i: number = 0; i < data.length; i++) {
    const idx: number = model.indexOf(data[i]);
    encoder.encode(model.cum[idx], model.cum[idx + 1], model.total);
  }
  encoder.finish();
  const body: Uint8Array = writer.finish();

  const out: Uint8Array = new Uint8Array(header.length + body.length);
  out.set(header, 0);
  out.set(body, header.length);
  return out;
}

export function decompress(packed: Uint8Array): Uint8Array {
  const cursor: ByteCursor = new ByteCursor(packed);
  const count: number = cursor.readVarint();
  const nsyms: number = cursor.readVarint();
  const symbols: number[] = [];
  const freqs: number[] = [];
  for (let i: number = 0; i < nsyms; i++) {
    symbols.push(cursor.readByte());
    freqs.push(cursor.readVarint());
  }
  const model: FrequencyModel = new FrequencyModel(symbols, freqs);

  const reader: BitReader = new BitReader(packed.subarray(cursor.pos));
  const decoder: ArithmeticDecoder = new ArithmeticDecoder(reader);
  const out: Uint8Array = new Uint8Array(count);
  for (let i: number = 0; i < count; i++) {
    const idx: number = model.findIndex(decoder.scale(model.total));
    out[i] = model.symbols[idx];
    decoder.update(model.cum[idx], model.cum[idx + 1], model.total);
  }
  return out;
}
