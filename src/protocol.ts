/**
 * Thrift Binary Protocol 线上类型常量、严格解码错误与底层字节读写器。
 * 参考 Apache Thrift TBinaryProtocol 的线格式，仅支持 strict message envelope。
 */

export const ThriftType = {
  STOP: 0,
  BOOL: 2,
  BYTE: 3,
  I16: 4,
  I32: 5,
  I64: 6,
  DOUBLE: 7,
  STRING: 8,
  STRUCT: 12,
  MAP: 13,
  SET: 14,
  LIST: 15,
} as const;
export type ThriftType = (typeof ThriftType)[keyof typeof ThriftType];

export const ThriftMessageType = {
  CALL: 1,
  REPLY: 2,
  EXCEPTION: 3,
  ONEWAY: 4,
} as const;
export type ThriftMessageType = (typeof ThriftMessageType)[keyof typeof ThriftMessageType];

/** 严格解码失败原因码，调用方按码做监控/拒绝即可，不要依赖 message 文案。 */
export type ThriftErrorCode =
  | 'MESSAGE_TOO_LARGE'
  | 'INVALID_MESSAGE'
  | 'TRUNCATED_INPUT'
  | 'TRAILING_BYTES'
  | 'INVALID_TYPE'
  | 'INVALID_FIELD_ID'
  | 'FIELD_TYPE_MISMATCH'
  | 'DUPLICATE_FIELD'
  | 'INVALID_LENGTH'
  | 'CONTAINER_LENGTH_TOO_LARGE'
  | 'NESTED_TOO_DEEP'
  | 'UNKNOWN_BYTES_EXCEEDED'
  | 'INVALID_ENCODING';

export class ThriftDecodeError extends Error {
  readonly code: ThriftErrorCode;
  /** 出错时读取器已经消费到的字节偏移，便于日志定位。 */
  readonly offset: number;

  constructor(code: ThriftErrorCode, message: string, offset: number) {
    super(`${code} at byte ${offset}: ${message}`);
    this.name = 'ThriftDecodeError';
    this.code = code;
    this.offset = offset;
  }
}

/** 带边界检查的只读字节游标，任何越界读取都抛 TRUNCATED_INPUT 而不是返回 undefined。 */
export class ByteReader {
  readonly bytes: Uint8Array;
  private readonly view: DataView;
  offset = 0;

  constructor(bytes: Uint8Array) {
    this.bytes = bytes;
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }

  get remaining(): number {
    return this.bytes.length - this.offset;
  }

  require(size: number): void {
    if (size < 0 || this.offset + size > this.bytes.length) {
      throw new ThriftDecodeError(
        'TRUNCATED_INPUT',
        `need ${size} byte(s), only ${this.remaining} byte(s) left`,
        this.offset,
      );
    }
  }

  readByte(): number {
    this.require(1);
    return this.bytes[this.offset++];
  }

  readI16(): number {
    this.require(2);
    const value = this.view.getInt16(this.offset);
    this.offset += 2;
    return value;
  }

  readI32(): number {
    this.require(4);
    const value = this.view.getInt32(this.offset);
    this.offset += 4;
    return value;
  }

  readU32(): number {
    this.require(4);
    const value = this.view.getUint32(this.offset);
    this.offset += 4;
    return value;
  }

  readI64(): bigint {
    this.require(8);
    const value = this.view.getBigInt64(this.offset);
    this.offset += 8;
    return value;
  }

  readDouble(): number {
    this.require(8);
    const value = this.view.getFloat64(this.offset);
    this.offset += 8;
    return value;
  }

  readRaw(length: number): Uint8Array {
    this.require(length);
    const slice = this.bytes.subarray(this.offset, this.offset + length);
    this.offset += length;
    return slice;
  }
}

/** 自动扩容的写字节游标，输出规范重编码字节。 */
export class ByteWriter {
  private bytes: Uint8Array;
  private view: DataView;
  private cursor = 0;

  constructor(initialCapacity = 128) {
    this.bytes = new Uint8Array(initialCapacity);
    this.view = new DataView(this.bytes.buffer);
  }

  get length(): number {
    return this.cursor;
  }

  private ensure(extra: number): void {
    const required = this.cursor + extra;
    if (required <= this.bytes.length) {
      return;
    }
    let capacity = this.bytes.length * 2;
    while (capacity < required) {
      capacity *= 2;
    }
    const next = new Uint8Array(capacity);
    next.set(this.bytes);
    this.bytes = next;
    // 必须基于新底层 ArrayBuffer 重建 DataView，否则多字节写入会落到旧缓冲区。
    this.view = new DataView(next.buffer, next.byteOffset, next.byteLength);
  }

  writeByte(value: number): void {
    this.ensure(1);
    this.bytes[this.cursor++] = value & 0xff;
  }

  writeI16(value: number): void {
    this.ensure(2);
    this.view.setInt16(this.cursor, value);
    this.cursor += 2;
  }

  writeI32(value: number): void {
    this.ensure(4);
    this.view.setInt32(this.cursor, value);
    this.cursor += 4;
  }

  writeI64(value: bigint): void {
    this.ensure(8);
    this.view.setBigInt64(this.cursor, value);
    this.cursor += 8;
  }

  writeDouble(value: number): void {
    this.ensure(8);
    this.view.setFloat64(this.cursor, value);
    this.cursor += 8;
  }

  writeBytes(value: Uint8Array): void {
    this.ensure(value.length);
    this.bytes.set(value, this.cursor);
    this.cursor += value.length;
  }

  toBytes(): Uint8Array {
    return this.bytes.slice(0, this.cursor);
  }
}

/**
 * 某种线上类型在 Binary 协议里的最小固定开销/最小元素尺寸（字节）。
 * 容器按“剩余字节 / 最小元素尺寸”预校验声明长度，使夸大长度在分配前即被拒绝。
 */
export function minimumWireSize(type: ThriftType): number {
  switch (type) {
    case ThriftType.BOOL:
    case ThriftType.BYTE:
      return 1;
    case ThriftType.I16:
      return 2;
    case ThriftType.I32:
      return 4;
    case ThriftType.I64:
    case ThriftType.DOUBLE:
      return 8;
    case ThriftType.STRING:
      // 长度前缀本身；实际元素至少 4 字节。
      return 4;
    case ThriftType.STRUCT:
      // 至少一个 STOP 字节。
      return 1;
    case ThriftType.LIST:
    case ThriftType.SET:
      // elemType 字节 + i32 长度。
      return 5;
    case ThriftType.MAP:
      // keyType + valueType + i32 长度。
      return 6;
    default:
      return Number.POSITIVE_INFINITY;
  }
}

export function isValidWireType(type: number): type is ThriftType {
  return (
    type === ThriftType.BOOL ||
    type === ThriftType.BYTE ||
    type === ThriftType.I16 ||
    type === ThriftType.I32 ||
    type === ThriftType.I64 ||
    type === ThriftType.DOUBLE ||
    type === ThriftType.STRING ||
    type === ThriftType.STRUCT ||
    type === ThriftType.MAP ||
    type === ThriftType.SET ||
    type === ThriftType.LIST
  );
}

const utf8Encoder = new TextEncoder();
const strictUtf8Decoder = new TextDecoder('utf-8', { fatal: true });

export function utf8Encode(value: string): Uint8Array {
  return utf8Encoder.encode(value);
}

export function utf8Decode(value: Uint8Array): string {
  try {
    return strictUtf8Decoder.decode(value);
  } catch {
    throw new ThriftDecodeError('INVALID_ENCODING', 'invalid UTF-8 bytes', 0);
  }
}
