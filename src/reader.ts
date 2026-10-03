import { ProtobufDecodeError } from './errors.ts';
import { decodeVarint, zigzagDecode32, zigzagDecode64, MAX_VARINT_BYTES } from './varint.ts';

/** Protobuf wire types。 */
export const WIRE_VARINT = 0;
export const WIRE_FIXED64 = 1;
export const WIRE_LENGTH_DELIMITED = 2;
export const WIRE_FIXED32 = 5;

export interface Tag {
  fieldNumber: number;
  wireType: number;
}

/**
 * 边界安全的二进制读取器。所有读取均做越界检查，
 * 截断时抛出携带偏移量的 ProtobufDecodeError，绝不产生未定义行为。
 */
export class WireReader {
  private readonly buf: Uint8Array;
  private pos: number;
  private readonly limit: number;

  constructor(buf: Uint8Array, offset = 0, length?: number) {
    const end = length === undefined ? buf.length : offset + length;
    if (offset < 0 || end > buf.length || end < offset) {
      throw new ProtobufDecodeError('Reader window out of bounds', offset);
    }
    this.buf = buf;
    this.pos = offset;
    this.limit = end;
  }

  get offset(): number {
    return this.pos;
  }

  get remaining(): number {
    return this.limit - this.pos;
  }

  get eof(): boolean {
    return this.pos >= this.limit;
  }

  private need(n: number): void {
    if (this.pos + n > this.limit) {
      throw new ProtobufDecodeError(
        `Truncated field: need ${n} byte(s), only ${this.limit - this.pos} remain`,
        this.pos,
      );
    }
  }

  readTag(): Tag {
    const start = this.pos;
    const { value, bytesRead } = decodeVarint(this.buf.subarray(0, this.limit), this.pos);
    this.pos += bytesRead;
    const tag = Number(value & 0xffffffffn);
    const wireType = tag & 0x7;
    const fieldNumber = tag >>> 3;
    if (fieldNumber === 0) {
      throw new ProtobufDecodeError('Invalid tag: field number 0', start);
    }
    if (wireType === 3 || wireType === 4 || wireType > 5) {
      throw new ProtobufDecodeError(`Unsupported wire type ${wireType}`, start);
    }
    return { fieldNumber, wireType };
  }

  readVarint(): bigint {
    const { value, bytesRead } = decodeVarint(this.buf.subarray(0, this.limit), this.pos);
    this.pos += bytesRead;
    return value;
  }

  readVarint32(): number {
    return Number(this.readVarint() & 0xffffffffn);
  }

  readSVarint32(): number {
    return zigzagDecode32(Number(this.readVarint() & 0xffffffffn));
  }

  readSVarint64(): bigint {
    return zigzagDecode64(this.readVarint());
  }

  readBool(): boolean {
    return this.readVarint() !== 0n;
  }

  readFixed32(): number {
    this.need(4);
    const p = this.pos;
    const v =
      (this.buf[p] | (this.buf[p + 1] << 8) | (this.buf[p + 2] << 16) | (this.buf[p + 3] << 24)) >>> 0;
    this.pos += 4;
    return v;
  }

  readFixed64(): bigint {
    this.need(8);
    const p = this.pos;
    let v = 0n;
    for (let i = 7; i >= 0; i--) {
      v = (v << 8n) | BigInt(this.buf[p + i]);
    }
    this.pos += 8;
    return v;
  }

  readFloat(): number {
    this.need(4);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 4).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8);
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.pos, 8).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  readBytes(n: number): Uint8Array {
    if (n < 0) throw new ProtobufDecodeError('Negative length', this.pos);
    this.need(n);
    const out = this.buf.subarray(this.pos, this.pos + n);
    this.pos += n;
    return out;
  }

  /** 读取 length-delimited 字段，返回限制在该段内的子读取器（用于嵌套消息）。 */
  readLengthDelimited(): WireReader {
    const len = Number(this.readVarint());
    if (len > this.remaining) {
      throw new ProtobufDecodeError(
        `Truncated length-delimited field: declared ${len} byte(s), only ${this.remaining} remain`,
        this.pos,
      );
    }
    const sub = new WireReader(this.buf, this.pos, len);
    this.pos += len;
    return sub;
  }

  /** 按 wire type 跳过未知字段，保证不越界。 */
  skipField(wireType: number): void {
    switch (wireType) {
      case WIRE_VARINT: {
        // 手动逐字节跳过，避免 decodeVarint 的子数组分配
        for (let i = 0; i < MAX_VARINT_BYTES; i++) {
          this.need(1);
          if ((this.buf[this.pos++] & 0x80) === 0) return;
        }
        throw new ProtobufDecodeError('Malformed varint while skipping', this.pos);
      }
      case WIRE_FIXED64:
        this.need(8);
        this.pos += 8;
        return;
      case WIRE_LENGTH_DELIMITED: {
        const len = Number(this.readVarint());
        this.need(len);
        this.pos += len;
        return;
      }
      case WIRE_FIXED32:
        this.need(4);
        this.pos += 4;
        return;
      default:
        throw new ProtobufDecodeError(`Cannot skip wire type ${wireType}`, this.pos);
    }
  }
}
