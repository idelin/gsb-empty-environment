/**
 * Protocol Buffers 二进制 Wire-format 核心编解码器（零依赖，纯手工解析）。
 * 支持 Varint(7位压缩) / ZigZag / Fixed32 / Fixed64 / Length-delimited。
 * 所有读取均做安全越界防护，字段截断时抛出 TruncatedFieldError。
 */

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export class CodecError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

/** 字段截断 / 安全越界防护错误：携带字段号与期望/剩余字节信息。 */
export class TruncatedFieldError extends CodecError {
  readonly needed: number;
  readonly remaining: number;
  readonly fieldNumber: number | null;
  constructor(what: string, needed: number, remaining: number, fieldNumber: number | null = null) {
    super(
      `字段截断(安全越界防护): ${what} 需要 ${needed} 字节，但缓冲区仅剩 ${remaining} 字节` +
        (fieldNumber !== null ? ` (field #${fieldNumber})` : ''),
    );
    this.needed = needed;
    this.remaining = remaining;
    this.fieldNumber = fieldNumber;
  }
}

export class MalformedVarintError extends CodecError {}

const MASK_64 = (1n << 64n) - 1n;
const MAX_VARINT_BYTES = 10; // 64bit / 7bit => 最多 10 字节

/** Varint 编码：7 位一组，小端序，最高位为续位标志。接受 number 或 bigint。 */
export function encodeVarint(value: number | bigint): Uint8Array {
  let v = BigInt(value) & MASK_64; // 负数按 int64 补码处理
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

/** ZigZag：有符号 -> 无符号。(n << 1) ^ (n >> 63) */
export function zigzagEncode(value: number | bigint): bigint {
  const n = BigInt(value);
  return ((n << 1n) ^ (n >> 63n)) & MASK_64;
}

/** ZigZag 解码：无符号 -> 有符号。 */
export function zigzagDecode(value: bigint): bigint {
  return (value >> 1n) ^ -(value & 1n);
}

/** 边界安全的只读字节游标。任何越界访问都会抛出 TruncatedFieldError，绝不越界读。 */
export class ByteReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly end: number;
  fieldNumber: number | null = null; // 由上层解码器设置，用于错误定位

  constructor(buf: Uint8Array, offset = 0, length = buf.length - offset) {
    if (offset < 0 || length < 0 || offset + length > buf.length) {
      throw new TruncatedFieldError('reader 初始化窗口', offset + length, buf.length);
    }
    this.buf = buf;
    this.pos = offset;
    this.end = offset + length;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get eof(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number, what: string): void {
    if (this.remaining < n) {
      throw new TruncatedFieldError(what, n, this.remaining, this.fieldNumber);
    }
  }

  readByte(): number {
    this.need(1, 'byte');
    return this.buf[this.pos++];
  }

  /** 读取 Varint，返回 bigint（保证 64 位精度）。 */
  readVarint(): bigint {
    let result = 0n;
    for (let shift = 0n; shift < 70n; shift += 7n) {
      this.need(1, 'varint');
      const byte = this.buf[this.pos++];
      result |= BigInt(byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) {
        if (shift >= 64n && result > MASK_64) {
          throw new MalformedVarintError('varint 超出 64 位范围');
        }
        return result & MASK_64;
      }
    }
    throw new MalformedVarintError(`varint 超过最大 ${MAX_VARINT_BYTES} 字节仍未终止`);
  }

  readVarintAsNumber(): number {
    const v = this.readVarint();
    if (v > BigInt(Number.MAX_SAFE_INTEGER)) {
      throw new CodecError(`varint ${v} 超出 Number 安全整数范围`);
    }
    return Number(v);
  }

  readZigZag(): bigint {
    return zigzagDecode(this.readVarint());
  }

  readFixed32(): number {
    this.need(4, 'fixed32');
    const p = this.pos;
    this.pos += 4;
    return (
      (this.buf[p] | (this.buf[p + 1] << 8) | (this.buf[p + 2] << 16) | (this.buf[p + 3] << 24)) >>> 0
    );
  }

  readFixed64(): bigint {
    this.need(8, 'fixed64');
    let v = 0n;
    for (let i = 0; i < 8; i++) {
      v |= BigInt(this.buf[this.pos + i]) << BigInt(8 * i);
    }
    this.pos += 8;
    return v;
  }

  /** 读取 Length-delimited 负载（先 varint 长度，再取字节）。 */
  readBytes(): Uint8Array {
    const len = this.readVarintAsNumber();
    this.need(len, 'length-delimited payload');
    const out = this.buf.subarray(this.pos, this.pos + len);
    this.pos += len;
    return out;
  }

  readString(): string {
    return new TextDecoder().decode(this.readBytes());
  }

  /** 读取 tag：返回 [fieldNumber, wireType]。 */
  readTag(): [number, WireTypeValue] {
    const key = this.readVarintAsNumber();
    const wireType = (key & 0x7) as WireTypeValue;
    const fieldNumber = key >>> 3;
    if (fieldNumber === 0) throw new CodecError('非法 tag: 字段号不能为 0');
    if (
      wireType !== WireType.Varint &&
      wireType !== WireType.Fixed64 &&
      wireType !== WireType.LengthDelimited &&
      wireType !== WireType.Fixed32
    ) {
      throw new CodecError(`不支持的 wire type: ${wireType} (field #${fieldNumber})`);
    }
    this.fieldNumber = fieldNumber;
    return [fieldNumber, wireType];
  }

  /** 派生一个限定在指定长度内的子 reader（用于嵌套消息）。 */
  subReader(len: number): ByteReader {
    this.need(len, 'sub-message');
    const r = new ByteReader(this.buf, this.pos, len);
    this.pos += len;
    return r;
  }
}

/** 追加式字节写出器。 */
export class ByteWriter {
  private chunks: Uint8Array[] = [];
  private size = 0;

  writeVarint(value: number | bigint): this {
    return this.writeBytes(encodeVarint(value));
  }

  writeZigZag(value: number | bigint): this {
    return this.writeVarint(zigzagEncode(value));
  }

  writeFixed32(value: number): this {
    const b = new Uint8Array(4);
    const v = value >>> 0;
    b[0] = v & 0xff;
    b[1] = (v >>> 8) & 0xff;
    b[2] = (v >>> 16) & 0xff;
    b[3] = (v >>> 24) & 0xff;
    return this.writeBytes(b);
  }

  writeFixed64(value: number | bigint): this {
    const b = new Uint8Array(8);
    let v = BigInt(value) & MASK_64;
    for (let i = 0; i < 8; i++) {
      b[i] = Number(v & 0xffn);
      v >>= 8n;
    }
    return this.writeBytes(b);
  }

  writeBytes(bytes: Uint8Array): this {
    this.chunks.push(bytes);
    this.size += bytes.length;
    return this;
  }

  writeLengthDelimited(bytes: Uint8Array): this {
    this.writeVarint(bytes.length);
    return this.writeBytes(bytes);
  }

  writeString(s: string): this {
    return this.writeLengthDelimited(new TextEncoder().encode(s));
  }

  writeTag(fieldNumber: number, wireType: WireTypeValue): this {
    if (!Number.isInteger(fieldNumber) || fieldNumber <= 0) {
      throw new CodecError(`非法字段号: ${fieldNumber}`);
    }
    return this.writeVarint((fieldNumber << 3) | wireType);
  }

  concat(): Uint8Array {
    const out = new Uint8Array(this.size);
    let off = 0;
    for (const c of this.chunks) {
      out.set(c, off);
      off += c.length;
    }
    return out;
  }
}
