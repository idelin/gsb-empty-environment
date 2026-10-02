import { ProtobufWireError } from './errors.ts';

/** Protocol Buffers wire types. */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireType = (typeof WireType)[keyof typeof WireType];

export function makeTag(fieldNumber: number, wireType: WireType): number {
  if (!Number.isInteger(fieldNumber) || fieldNumber <= 0) {
    throw new ProtobufWireError(`invalid field number ${fieldNumber}`);
  }
  return (fieldNumber << 3) | wireType;
}

export function parseTag(key: bigint): { fieldNumber: number; wireType: WireType } {
  const wireType = Number(key & 0x7n) as WireType;
  if (
    wireType !== WireType.Varint &&
    wireType !== WireType.Fixed64 &&
    wireType !== WireType.LengthDelimited &&
    wireType !== WireType.Fixed32
  ) {
    throw new ProtobufWireError(`unsupported wire type ${wireType}`);
  }
  const fieldNumber = Number(key >> 3n);
  if (fieldNumber <= 0) {
    throw new ProtobufWireError(`invalid field number ${fieldNumber}`);
  }
  return { fieldNumber, wireType };
}
