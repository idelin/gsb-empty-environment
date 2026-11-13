import { ChallengeParseError } from "./errors.ts";

export const CBOR_MAJOR_UINT = 0;
export const CBOR_MAJOR_BYTES = 2;
export const CBOR_MAJOR_TEXT = 3;
export const CBOR_MAJOR_ARRAY = 4;

const TEXT_ENCODER = new TextEncoder();
const TEXT_DECODER = new TextDecoder("utf-8", { fatal: true });

export type CborValue =
  | number
  | string
  | Uint8Array
  | CborValue[];

export function encodeCborCanonical(value: CborValue): Uint8Array {
  const writer = new CborWriter();
  writer.writeValue(value);
  return writer.toBytes();
}

export function decodeCborCanonical(input: Uint8Array): CborValue {
  const reader = new CborReader(input);
  const value = reader.readValue();
  if (!reader.atEnd()) {
    throw new ChallengeParseError("CBOR input contains trailing bytes");
  }
  return value;
}

class CborReader {
  private offset = 0;
  private readonly input: Uint8Array;

  constructor(input: Uint8Array) {
    this.input = input;
  }

  atEnd(): boolean {
    return this.offset === this.input.length;
  }

  readValue(): CborValue {
    const initial = this.readByte();
    const majorType = initial >> 5;
    const additionalInfo = initial & 0x1f;

    switch (majorType) {
      case CBOR_MAJOR_UINT:
        return Number(this.readArgument(additionalInfo));
      case CBOR_MAJOR_BYTES:
        return this.readLengthPrefixed(additionalInfo);
      case CBOR_MAJOR_TEXT:
        return this.decodeText(this.readLengthPrefixed(additionalInfo));
      case CBOR_MAJOR_ARRAY:
        return this.readArray(additionalInfo);
      default:
        throw new ChallengeParseError(
          `Unsupported CBOR major type ${majorType} in TokenChallenge`,
        );
    }
  }

  private readArray(additionalInfo: number): CborValue[] {
    if (additionalInfo === 31) {
      throw new ChallengeParseError("Indefinite-length CBOR arrays are not canonical");
    }
    const length = Number(this.readArgument(additionalInfo));
    const items: CborValue[] = [];
    for (let index = 0; index < length; index += 1) {
      items.push(this.readValue());
    }
    return items;
  }

  private readLengthPrefixed(additionalInfo: number): Uint8Array {
    if (additionalInfo === 31) {
      throw new ChallengeParseError("Indefinite-length CBOR strings are not canonical");
    }
    const length = Number(this.readArgument(additionalInfo));
    if (this.offset + length > this.input.length) {
      throw new ChallengeParseError("CBOR value extends past the end of input");
    }
    const slice = this.input.subarray(this.offset, this.offset + length);
    this.offset += length;
    const copy = new Uint8Array(length);
    copy.set(slice);
    return copy;
  }

  private readArgument(additionalInfo: number): bigint {
    if (additionalInfo < 24) {
      return BigInt(additionalInfo);
    }
    if (additionalInfo === 24) {
      const value = this.readByte();
      if (value < 24) {
        throw new ChallengeParseError("Non-minimal CBOR integer encoding");
      }
      return BigInt(value);
    }
    if (additionalInfo === 25) {
      return this.readFixedWidth(2);
    }
    if (additionalInfo === 26) {
      return this.readFixedWidth(4);
    }
    if (additionalInfo === 27) {
      return this.readFixedWidth(8);
    }
    throw new ChallengeParseError("Reserved or unsupported CBOR argument encoding");
  }

  private readFixedWidth(byteLength: number): bigint {
    if (this.offset + byteLength > this.input.length) {
      throw new ChallengeParseError("CBOR integer extends past the end of input");
    }
    let value = 0n;
    for (let index = 0; index < byteLength; index += 1) {
      value = (value << 8n) | BigInt(this.input[this.offset] ?? 0);
      this.offset += 1;
    }
    const minimum = 1n << BigInt((byteLength - 1) * 8);
    if (value < minimum) {
      throw new ChallengeParseError("Non-minimal CBOR integer encoding");
    }
    return value;
  }

  private decodeText(encoded: Uint8Array): string {
    try {
      return TEXT_DECODER.decode(encoded);
    } catch (cause) {
      throw new ChallengeParseError("CBOR text string is not valid UTF-8", { cause });
    }
  }

  private readByte(): number {
    if (this.offset >= this.input.length) {
      throw new ChallengeParseError("Unexpected end of CBOR input");
    }
    const byte = this.input[this.offset] ?? 0;
    this.offset += 1;
    return byte;
  }
}

class CborWriter {
  private chunks: number[] = [];

  toBytes(): Uint8Array {
    return Uint8Array.from(this.chunks);
  }

  writeValue(value: CborValue): void {
    if (typeof value === "number") {
      this.writeUnsignedInteger(value);
      return;
    }
    if (typeof value === "string") {
      this.writeText(value);
      return;
    }
    if (value instanceof Uint8Array) {
      this.writeBytes(value);
      return;
    }
    if (Array.isArray(value)) {
      this.writeHead(CBOR_MAJOR_ARRAY, value.length);
      for (const item of value) {
        this.writeValue(item);
      }
      return;
    }
    throw new ChallengeParseError("Unsupported value type for canonical CBOR encoding");
  }

  private writeUnsignedInteger(value: number): void {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new ChallengeParseError("CBOR unsigned integer must be a non-negative safe integer");
    }
    this.writeHead(CBOR_MAJOR_UINT, value);
  }

  private writeBytes(value: Uint8Array): void {
    this.writeHead(CBOR_MAJOR_BYTES, value.length);
    for (const byte of value) {
      this.chunks.push(byte);
    }
  }

  private writeText(value: string): void {
    const encoded = TEXT_ENCODER.encode(value);
    this.writeHead(CBOR_MAJOR_TEXT, encoded.length);
    for (const byte of encoded) {
      this.chunks.push(byte);
    }
  }

  private writeHead(majorType: number, argument: number): void {
    const prefix = majorType << 5;
    if (argument < 24) {
      this.chunks.push(prefix | argument);
      return;
    }
    if (argument <= 0xff) {
      this.chunks.push(prefix | 24, argument);
      return;
    }
    if (argument <= 0xffff) {
      this.chunks.push(prefix | 25, argument >> 8, argument & 0xff);
      return;
    }
    if (argument <= 0xffffffff) {
      this.chunks.push(
        prefix | 26,
        (argument >>> 24) & 0xff,
        (argument >>> 16) & 0xff,
        (argument >>> 8) & 0xff,
        argument & 0xff,
      );
      return;
    }
    throw new ChallengeParseError("CBOR argument exceeds 32-bit unsigned range");
  }
}
