export class WasmParseError extends Error {
  readonly code = 'parse_error' as const;

  constructor(message: string) {
    super(message);
    this.name = 'WasmParseError';
  }
}

export class ByteReader {
  private offset = 0;
  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  get position(): number {
    return this.offset;
  }

  get remaining(): number {
    return this.data.length - this.offset;
  }

  eof(): boolean {
    return this.offset >= this.data.length;
  }

  u8(): number {
    if (this.offset >= this.data.length) {
      throw new WasmParseError('unexpected end of section or function body');
    }
    return this.data[this.offset++];
  }

  take(length: number): Uint8Array {
    if (length > this.remaining) {
      throw new WasmParseError('declared length exceeds remaining bytes');
    }
    const slice = this.data.subarray(this.offset, this.offset + length);
    this.offset += length;
    return slice;
  }

  readULEB128(): number {
    let result = 0;
    let shift = 0;
    for (let byteIndex = 0; byteIndex < 5; byteIndex++) {
      const byte = this.u8();
      result |= (byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) {
        if (byteIndex === 4 && (byte & 0xf0) !== 0) {
          throw new WasmParseError('unsigned LEB128 integer overflows 32 bits');
        }
        return result >>> 0;
      }
      shift += 7;
    }
    throw new WasmParseError('unterminated unsigned LEB128 integer');
  }

  readName(): string {
    const length = this.readULEB128();
    const raw = this.take(length);
    try {
      return new TextDecoder('utf-8', { fatal: true }).decode(raw);
    } catch {
      throw new WasmParseError('module or field name is not valid UTF-8');
    }
  }
}
