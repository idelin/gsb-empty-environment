const DEFAULT_MAX_DEPTH = 32;
const DEFAULT_MAX_ELEMENTS = 100_000;

const FIXED_EXTENSION_LENGTHS = new Set([1, 2, 4, 8, 16]);
const UTF8_DECODER = new TextDecoder("utf-8", {
  fatal: true,
  ignoreBOM: true,
});

export type MessagePackMap = { [key: string]: MessagePackValue };

export interface MessagePackExtension {
  readonly type: number;
  readonly data: Uint8Array;
}

export type MessagePackValue =
  | null
  | boolean
  | number
  | bigint
  | string
  | Uint8Array
  | MessagePackExtension
  | MessagePackValue[]
  | MessagePackMap;

export type ExtTypeWhitelist = readonly number[] | ReadonlySet<number>;

export interface StrictDecodeOptions {
  /**
   * Signed extension type numbers (-128..127) that the caller trusts.
   * No extension types are allowed when this option is omitted.
   */
  readonly allowedExtTypes?: ExtTypeWhitelist;
  /**
   * Maximum number of nested arrays/maps. A scalar root has depth 0.
   */
  readonly maxDepth?: number;
  /**
   * Maximum number of decoded values, including container nodes, map keys,
   * and map values.
   */
  readonly maxElements?: number;
}

export type StrictDecodeErrorCode =
  | "INVALID_INPUT"
  | "INVALID_OPTIONS"
  | "TRUNCATED"
  | "TRAILING_BYTES"
  | "UNSUPPORTED_MARKER"
  | "NON_CANONICAL_INTEGER"
  | "NON_CANONICAL_STRING"
  | "NON_CANONICAL_ARRAY"
  | "NON_CANONICAL_MAP"
  | "NON_CANONICAL_BINARY"
  | "NON_CANONICAL_EXTENSION"
  | "INVALID_TIMESTAMP_LENGTH"
  | "EXTENSION_TYPE_NOT_ALLOWED"
  | "INVALID_MAP_KEY"
  | "DUPLICATE_MAP_KEY"
  | "INVALID_UTF8"
  | "MAX_DEPTH_EXCEEDED"
  | "MAX_ELEMENTS_EXCEEDED";

export class StrictDecodeError extends Error {
  readonly code: StrictDecodeErrorCode;
  readonly offset: number;

  constructor(
    code: StrictDecodeErrorCode,
    message: string,
    offset: number,
  ) {
    super(`${message} (byte ${offset})`);
    this.name = "StrictDecodeError";
    this.code = code;
    this.offset = offset;
  }
}

interface NormalizedOptions {
  readonly allowedExtTypes: ReadonlySet<number>;
  readonly maxDepth: number;
  readonly maxElements: number;
}

class ByteReader {
  readonly bytes: Uint8Array;
  readonly view: DataView;
  offset = 0;

  constructor(bytes: Uint8Array) {
    this.bytes = bytes;
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }

  readU8(): number {
    this.ensure(1);
    return this.view.getUint8(this.offset++);
  }

  readI8(): number {
    this.ensure(1);
    return this.view.getInt8(this.offset++);
  }

  readU16(): number {
    this.ensure(2);
    const value = this.view.getUint16(this.offset, false);
    this.offset += 2;
    return value;
  }

  readI16(): number {
    this.ensure(2);
    const value = this.view.getInt16(this.offset, false);
    this.offset += 2;
    return value;
  }

  readU32(): number {
    this.ensure(4);
    const value = this.view.getUint32(this.offset, false);
    this.offset += 4;
    return value;
  }

  readI32(): number {
    this.ensure(4);
    const value = this.view.getInt32(this.offset, false);
    this.offset += 4;
    return value;
  }

  readU64(): bigint {
    const high = this.readU32();
    const low = this.readU32();
    return (BigInt(high) << 32n) | BigInt(low);
  }

  readI64(): bigint {
    const unsigned = this.readU64();
    return unsigned >= (1n << 63n) ? unsigned - (1n << 64n) : unsigned;
  }

  readFloat32(): number {
    this.ensure(4);
    const value = this.view.getFloat32(this.offset, false);
    this.offset += 4;
    return value;
  }

  readFloat64(): number {
    this.ensure(8);
    const value = this.view.getFloat64(this.offset, false);
    this.offset += 8;
    return value;
  }

  readBytes(length: number, errorOffset = this.offset): Uint8Array {
    this.ensure(length, errorOffset);
    const value = this.bytes.subarray(this.offset, this.offset + length);
    this.offset += length;
    return value;
  }

  private ensure(length: number, errorOffset = this.offset): void {
    if (length > this.bytes.byteLength - this.offset) {
      throw new StrictDecodeError(
        "TRUNCATED",
        `MessagePack payload ended ${length - (this.bytes.byteLength - this.offset)} byte(s) early`,
        errorOffset,
      );
    }
  }
}

function normalizeOptions(options: StrictDecodeOptions): NormalizedOptions {
  return {
    allowedExtTypes: normalizeExtTypes(options.allowedExtTypes),
    maxDepth: normalizeLimit("maxDepth", options.maxDepth, DEFAULT_MAX_DEPTH),
    maxElements: normalizeLimit(
      "maxElements",
      options.maxElements,
      DEFAULT_MAX_ELEMENTS,
    ),
  };
}

function normalizeExtTypes(whitelist: ExtTypeWhitelist | undefined): Set<number> {
  const allowed = new Set<number>();
  if (whitelist === undefined) {
    return allowed;
  }

  const value: unknown = whitelist;
  if (
    typeof value !== "object" ||
    value === null ||
    typeof (value as { [Symbol.iterator]?: unknown })[Symbol.iterator] !==
      "function"
  ) {
    throw new StrictDecodeError(
      "INVALID_OPTIONS",
      "allowedExtTypes must be an array or Set of signed extension type numbers",
      0,
    );
  }

  for (const rawType of value as Iterable<unknown>) {
    if (
      typeof rawType !== "number" ||
      !Number.isInteger(rawType) ||
      rawType < -128 ||
      rawType > 127
    ) {
      throw new StrictDecodeError(
        "INVALID_OPTIONS",
        "allowedExtTypes entries must be integers in the range -128..127",
        0,
      );
    }
    allowed.add(rawType);
  }

  return allowed;
}

function normalizeLimit(
  name: "maxDepth" | "maxElements",
  value: number | undefined,
  fallback: number,
): number {
  if (value === undefined) {
    return fallback;
  }
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new StrictDecodeError(
      "INVALID_OPTIONS",
      `${name} must be a non-negative safe integer`,
      0,
    );
  }
  return value;
}

function integerValue(value: bigint): number | bigint {
  if (
    value >= BigInt(Number.MIN_SAFE_INTEGER) &&
    value <= BigInt(Number.MAX_SAFE_INTEGER)
  ) {
    return Number(value);
  }
  return value;
}

class StrictDecoder {
  private readonly reader: ByteReader;
  private readonly options: NormalizedOptions;
  private elements = 0;

  constructor(bytes: Uint8Array, options: NormalizedOptions) {
    this.reader = new ByteReader(bytes);
    this.options = options;
  }

  decode(): MessagePackValue {
    const value = this.parseValue(0);
    if (this.reader.offset !== this.reader.bytes.byteLength) {
      throw new StrictDecodeError(
        "TRAILING_BYTES",
        "Input contains bytes after the root value",
        this.reader.offset,
      );
    }
    return value;
  }

  private parseValue(depth: number): MessagePackValue {
    const start = this.reader.offset;
    this.consumeElement(start);
    const marker = this.reader.readU8();

    if (marker <= 0x7f) {
      return marker;
    }
    if (marker >= 0xe0) {
      return marker - 0x100;
    }
    if ((marker & 0xf0) === 0x80) {
      return this.parseMap(marker & 0x0f, depth, start);
    }
    if ((marker & 0xf0) === 0x90) {
      return this.parseArray(marker & 0x0f, depth, start);
    }
    if ((marker & 0xe0) === 0xa0) {
      return this.parseString(marker, start);
    }

    switch (marker) {
      case 0xc0:
        return null;
      case 0xc1:
        throw new StrictDecodeError(
          "UNSUPPORTED_MARKER",
          "0xc1 is not a valid MessagePack marker",
          start,
        );
      case 0xc2:
        return false;
      case 0xc3:
        return true;
      case 0xc4:
        return this.parseBinary(0xc4, start);
      case 0xc5:
        return this.parseBinary(0xc5, start);
      case 0xc6:
        return this.parseBinary(0xc6, start);
      case 0xc7:
        return this.parseExtension(0xc7, start);
      case 0xc8:
        return this.parseExtension(0xc8, start);
      case 0xc9:
        return this.parseExtension(0xc9, start);
      case 0xca:
        return this.reader.readFloat32();
      case 0xcb:
        return this.reader.readFloat64();
      case 0xcc: {
        const value = this.reader.readU8();
        if (value <= 0x7f) {
          this.nonCanonicalInteger(start, "positive fixint");
        }
        return value;
      }
      case 0xcd: {
        const value = this.reader.readU16();
        if (value <= 0xff) {
          this.nonCanonicalInteger(start, "uint8");
        }
        return value;
      }
      case 0xce: {
        const value = this.reader.readU32();
        if (value <= 0xffff) {
          this.nonCanonicalInteger(start, "uint16");
        }
        return value;
      }
      case 0xcf: {
        const value = this.reader.readU64();
        if (value <= 0xffffffffn) {
          this.nonCanonicalInteger(start, "uint32");
        }
        return integerValue(value);
      }
      case 0xd0: {
        const value = this.reader.readI8();
        if (value >= -32) {
          this.nonCanonicalInteger(start, "negative fixint or uint");
        }
        return value;
      }
      case 0xd1: {
        const value = this.reader.readI16();
        if (value >= -128) {
          this.nonCanonicalInteger(start, "int8 or a non-negative encoding");
        }
        return value;
      }
      case 0xd2: {
        const value = this.reader.readI32();
        if (value >= -32768) {
          this.nonCanonicalInteger(start, "int16 or a non-negative encoding");
        }
        return value;
      }
      case 0xd3: {
        const value = this.reader.readI64();
        if (value >= -2147483648n) {
          this.nonCanonicalInteger(start, "int32 or a non-negative encoding");
        }
        return integerValue(value);
      }
      case 0xd4:
        return this.parseExtension(0xd4, start);
      case 0xd5:
        return this.parseExtension(0xd5, start);
      case 0xd6:
        return this.parseExtension(0xd6, start);
      case 0xd7:
        return this.parseExtension(0xd7, start);
      case 0xd8:
        return this.parseExtension(0xd8, start);
      case 0xd9:
        return this.parseString(0xd9, start);
      case 0xda:
        return this.parseString(0xda, start);
      case 0xdb:
        return this.parseString(0xdb, start);
      case 0xdc: {
        const length = this.reader.readU16();
        if (length < 16) {
          this.nonCanonicalContainer("array", "fixarray", start);
        }
        return this.parseArray(length, depth, start);
      }
      case 0xdd: {
        const length = this.reader.readU32();
        if (length < 65536) {
          this.nonCanonicalContainer("array", "array16", start);
        }
        return this.parseArray(length, depth, start);
      }
      case 0xde: {
        const length = this.reader.readU16();
        if (length < 16) {
          this.nonCanonicalContainer("map", "fixmap", start);
        }
        return this.parseMap(length, depth, start);
      }
      case 0xdf: {
        const length = this.reader.readU32();
        if (length < 65536) {
          this.nonCanonicalContainer("map", "map16", start);
        }
        return this.parseMap(length, depth, start);
      }
      default:
        throw new StrictDecodeError(
          "UNSUPPORTED_MARKER",
          `Unsupported MessagePack marker 0x${marker.toString(16)}`,
          start,
        );
    }
  }

  private parseArray(
    length: number,
    depth: number,
    start: number,
  ): MessagePackValue[] {
    this.assertDepth(depth, start);
    if (length > this.options.maxElements - this.elements) {
      this.exceededElements(start);
    }

    const result: MessagePackValue[] = [];
    for (let index = 0; index < length; index += 1) {
      result.push(this.parseValue(depth + 1));
    }
    return result;
  }

  private parseMap(
    length: number,
    depth: number,
    start: number,
  ): MessagePackMap {
    this.assertDepth(depth, start);
    if (length > Math.floor((this.options.maxElements - this.elements) / 2)) {
      this.exceededElements(start);
    }

    const result: MessagePackMap = {};
    const keys = new Set<string>();

    for (let index = 0; index < length; index += 1) {
      const keyStart = this.reader.offset;
      const key = this.parseMapKey();
      if (keys.has(key)) {
        throw new StrictDecodeError(
          "DUPLICATE_MAP_KEY",
          "Map contains a duplicate string key",
          keyStart,
        );
      }
      keys.add(key);

      const value = this.parseValue(depth + 1);
      Object.defineProperty(result, key, {
        value,
        enumerable: true,
        configurable: true,
        writable: true,
      });
    }

    return result;
  }

  private parseMapKey(): string {
    const start = this.reader.offset;
    this.consumeElement(start);
    const marker = this.reader.readU8();

    if (
      !((marker & 0xe0) === 0xa0 ||
        marker === 0xd9 ||
        marker === 0xda ||
        marker === 0xdb)
    ) {
      throw new StrictDecodeError(
        "INVALID_MAP_KEY",
        "Map keys must be strings",
        start,
      );
    }

    return this.parseString(marker, start);
  }

  private parseString(marker: number, start: number): string {
    let length: number;

    if ((marker & 0xe0) === 0xa0) {
      length = marker & 0x1f;
    } else {
      switch (marker) {
        case 0xd9:
          length = this.reader.readU8();
          if (length < 32) {
            this.nonCanonicalContainer("string", "fixstr", start);
          }
          break;
        case 0xda:
          length = this.reader.readU16();
          if (length < 256) {
            this.nonCanonicalContainer("string", "str8", start);
          }
          break;
        case 0xdb:
          length = this.reader.readU32();
          if (length < 65536) {
            this.nonCanonicalContainer("string", "str16", start);
          }
          break;
        default:
          throw new StrictDecodeError(
            "INVALID_MAP_KEY",
            "Map keys must be strings",
            start,
          );
      }
    }

    const bytes = this.reader.readBytes(length, start);
    try {
      return UTF8_DECODER.decode(bytes);
    } catch {
      throw new StrictDecodeError(
        "INVALID_UTF8",
        "String payload is not valid UTF-8",
        start,
      );
    }
  }

  private parseBinary(marker: number, start: number): Uint8Array {
    let length: number;

    switch (marker) {
      case 0xc4:
        length = this.reader.readU8();
        break;
      case 0xc5:
        length = this.reader.readU16();
        if (length < 256) {
          this.nonCanonicalContainer("binary", "bin8", start);
        }
        break;
      case 0xc6:
        length = this.reader.readU32();
        if (length < 65536) {
          this.nonCanonicalContainer("binary", "bin16", start);
        }
        break;
      default:
        throw new StrictDecodeError(
          "UNSUPPORTED_MARKER",
          `Unsupported binary marker 0x${marker.toString(16)}`,
          start,
        );
    }

    return this.reader.readBytes(length, start);
  }

  private parseExtension(marker: number, start: number): MessagePackExtension {
    let length: number;

    switch (marker) {
      case 0xd4:
        length = 1;
        break;
      case 0xd5:
        length = 2;
        break;
      case 0xd6:
        length = 4;
        break;
      case 0xd7:
        length = 8;
        break;
      case 0xd8:
        length = 16;
        break;
      case 0xc7:
        length = this.reader.readU8();
        if (FIXED_EXTENSION_LENGTHS.has(length)) {
          this.nonCanonicalExtension(start);
        }
        break;
      case 0xc8:
        length = this.reader.readU16();
        if (length < 256) {
          this.nonCanonicalExtension(start);
        }
        break;
      case 0xc9:
        length = this.reader.readU32();
        if (length < 65536) {
          this.nonCanonicalExtension(start);
        }
        break;
      default:
        throw new StrictDecodeError(
          "UNSUPPORTED_MARKER",
          `Unsupported extension marker 0x${marker.toString(16)}`,
          start,
        );
    }

    const typeOffset = this.reader.offset;
    const type = this.reader.readI8();
    if (!this.options.allowedExtTypes.has(type)) {
      throw new StrictDecodeError(
        "EXTENSION_TYPE_NOT_ALLOWED",
        `Extension type ${type} is not allowed by this caller`,
        typeOffset,
      );
    }
    if (type === -1 && length !== 4 && length !== 8 && length !== 12) {
      throw new StrictDecodeError(
        "INVALID_TIMESTAMP_LENGTH",
        "Timestamp extension payload must be 4, 8, or 12 bytes",
        start,
      );
    }

    const data = this.reader.readBytes(length, start);
    return Object.freeze({ type, data });
  }

  private consumeElement(offset: number): void {
    if (this.elements >= this.options.maxElements) {
      this.exceededElements(offset);
    }
    this.elements += 1;
  }

  private assertDepth(depth: number, offset: number): void {
    if (depth >= this.options.maxDepth) {
      throw new StrictDecodeError(
        "MAX_DEPTH_EXCEEDED",
        `Container depth exceeds maxDepth (${this.options.maxDepth})`,
        offset,
      );
    }
  }

  private exceededElements(offset: number): never {
    throw new StrictDecodeError(
      "MAX_ELEMENTS_EXCEEDED",
      `Decoded value count exceeds maxElements (${this.options.maxElements})`,
      offset,
    );
  }

  private nonCanonicalInteger(offset: number, expected: string): never {
    throw new StrictDecodeError(
      "NON_CANONICAL_INTEGER",
      `Integer is not minimally encoded; use ${expected}`,
      offset,
    );
  }

  private nonCanonicalContainer(
    kind: "string" | "array" | "map" | "binary",
    expected: string,
    offset: number,
  ): never {
    const code: StrictDecodeErrorCode =
      kind === "string"
        ? "NON_CANONICAL_STRING"
        : kind === "array"
          ? "NON_CANONICAL_ARRAY"
          : kind === "map"
            ? "NON_CANONICAL_MAP"
            : "NON_CANONICAL_BINARY";
    throw new StrictDecodeError(
      code,
      `${kind} is not minimally encoded; use ${expected}`,
      offset,
    );
  }

  private nonCanonicalExtension(offset: number): never {
    throw new StrictDecodeError(
      "NON_CANONICAL_EXTENSION",
      "Extension length is not minimally encoded",
      offset,
    );
  }
}

function normalizeInput(input: Uint8Array | ArrayBuffer): Uint8Array {
  if (input instanceof Uint8Array) {
    return input;
  }
  if (input instanceof ArrayBuffer) {
    return new Uint8Array(input);
  }
  throw new StrictDecodeError(
    "INVALID_INPUT",
    "Input must be a Uint8Array or ArrayBuffer",
    0,
  );
}

/**
 * Decode exactly one MessagePack value using strict canonical-encoding and
 * resource-limit checks.
 */
export function decodeStrict(
  input: Uint8Array | ArrayBuffer,
  options: StrictDecodeOptions = {},
): MessagePackValue {
  const bytes = normalizeInput(input);
  const decoder = new StrictDecoder(bytes, normalizeOptions(options));
  return decoder.decode();
}

export const decodeMessagePackStrict = decodeStrict;
