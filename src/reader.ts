import { ProtobufDecodeError } from './errors.ts';
import { decodeVarint, zigzagDecode32, zigzagDecode64 } from './varint.ts';

export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  StartGroup: 3,
  EndGroup: 4,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export const MAX_FIELD_NUMBER = 0x1fffffff; // 2^29 - 1
const MAX_GROUP_DEPTH = 100;

const utf8Decoder = new TextDecoder('utf-8', { fatal: true });

export interface Tag {
  fieldNumber: number;
  wireType: number;
}

export class WireReader {
  readonly view: Uint8Array;
  readonly end: number;
  pos: number;

  constructor(view: Uint8Array, offset = 0, length = view.length - offset) {
    if (offset < 0 || length < 0 || offset + length > view.length) {
      throw new ProtobufDecodeError('OUT_OF_BOUNDS', 'reader window lies outside the buffer');
    }
    this.view = view;
    this.pos = offset;
    this.end = offset + length;
  }

  get remaining(): number {
    return this.end - this.pos;
  }

  get isAtEnd(): boolean {
    return this.pos >= this.end;
  }

  private need(n: number): void {
    if (n > this.remaining) {
      throw new ProtobufDecodeError(
        'TRUNCATED',
        `need ${n} byte(s) but only ${this.remaining} remain`,
        this.pos,
      );
    }
  }

  readVarint64(): bigint {
    const { value, offset } = decodeVarint(this.view, this.pos, this.end);
    this.pos = offset;
    return value;
  }

  readVarint32(): number {
    return Number(this.readVarint64() & 0xffff_ffffn) | 0;
  }

  readUint32(): number {
    return Number(this.readVarint64() & 0xffff_ffffn) >>> 0;
  }

  readInt64(): bigint {
    return BigInt.asIntN(64, this.readVarint64());
  }

  readBool(): boolean {
    return this.readVarint64() !== 0n;
  }

  readZigZag32(): number {
    return zigzagDecode32(this.readUint32());
  }

  readZigZag64(): bigint {
    return zigzagDecode64(this.readVarint64());
  }

  readFixed32(): number {
    this.need(4);
    const v =
      this.view[this.pos] |
      (this.view[this.pos + 1] << 8) |
      (this.view[this.pos + 2] << 16) |
      (this.view[this.pos + 3] << 24);
    this.pos += 4;
    return v >>> 0;
  }

  readSFixed32(): number {
    return this.readFixed32() | 0;
  }

  readFixed64(): bigint {
    this.need(8);
    let v = 0n;
    for (let i = 7; i >= 0; i -= 1) {
      v = (v << 8n) | BigInt(this.view[this.pos + i]);
    }
    this.pos += 8;
    return v;
  }

  readSFixed64(): bigint {
    return BigInt.asIntN(64, this.readFixed64());
  }

  readFloat(): number {
    this.need(4);
    const v = new DataView(this.view.buffer, this.view.byteOffset + this.pos, 4).getFloat32(0, true);
    this.pos += 4;
    return v;
  }

  readDouble(): number {
    this.need(8);
    const v = new DataView(this.view.buffer, this.view.byteOffset + this.pos, 8).getFloat64(0, true);
    this.pos += 8;
    return v;
  }

  readLength(): number {
    const len = this.readVarint64();
    if (len > BigInt(this.remaining)) {
      throw new ProtobufDecodeError(
        'OUT_OF_BOUNDS',
        `length-delimited field declares ${len} byte(s) but only ${this.remaining} remain`,
        this.pos,
      );
    }
    return Number(len);
  }

  readBytes(): Uint8Array {
    const len = this.readLength();
    const out = this.view.subarray(this.pos, this.pos + len); // zero-copy view
    this.pos += len;
    return out;
  }

  readString(): string {
    const bytes = this.readBytes();
    try {
      return utf8Decoder.decode(bytes);
    } catch {
      throw new ProtobufDecodeError('INVALID_UTF8', 'string field contains invalid UTF-8', this.pos);
    }
  }

  fork(): WireReader {
    const len = this.readLength();
    const sub = new WireReader(this.view, this.pos, len);
    this.pos += len;
    return sub;
  }

  readTag(): Tag {
    const tag = this.readVarint64();
    if (tag > 0xffff_ffffn) {
      throw new ProtobufDecodeError('INVALID_TAG', 'tag varint exceeds 32 bits', this.pos);
    }
    const t = Number(tag);
    const wireType = t & 7;
    const fieldNumber = t >>> 3;
    if (fieldNumber === 0 || fieldNumber > MAX_FIELD_NUMBER) {
      throw new ProtobufDecodeError('INVALID_TAG', `illegal field number ${fieldNumber}`, this.pos);
    }
    if (wireType === 6 || wireType === 7) {
      throw new ProtobufDecodeError('INVALID_WIRE_TYPE', `reserved wire type ${wireType}`, this.pos);
    }
    return { fieldNumber, wireType };
  }

  skipField(wireType: number, fieldNumber: number, depth = 0): void {
    switch (wireType) {
      case WireType.Varint:
        this.readVarint64();
        return;
      case WireType.Fixed64:
        this.need(8);
        this.pos += 8;
        return;
      case WireType.LengthDelimited: {
        const len = this.readLength();
        this.pos += len;
        return;
      }
      case WireType.Fixed32:
        this.need(4);
        this.pos += 4;
        return;
      case WireType.StartGroup: {
        if (depth >= MAX_GROUP_DEPTH) {
          throw new ProtobufDecodeError('RECURSION_LIMIT', 'group nesting too deep', this.pos);
        }
        for (;;) {
          if (this.isAtEnd) {
            throw new ProtobufDecodeError('TRUNCATED', 'unterminated group', this.pos);
          }
          const inner = this.readTag();
          if (inner.wireType === WireType.EndGroup) {
            if (inner.fieldNumber !== fieldNumber) {
              throw new ProtobufDecodeError(
                'INVALID_TAG',
                `end-group tag ${inner.fieldNumber} does not match start-group tag ${fieldNumber}`,
                this.pos,
              );
            }
            return;
          }
          this.skipField(inner.wireType, inner.fieldNumber, depth + 1);
        }
      }
      case WireType.EndGroup:
        throw new ProtobufDecodeError('INVALID_WIRE_TYPE', 'unexpected end-group tag', this.pos);
      default:
        throw new ProtobufDecodeError('INVALID_WIRE_TYPE', `unknown wire type ${wireType}`, this.pos);
    }
  }
}
