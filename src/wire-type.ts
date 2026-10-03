/** Protobuf wire types (type-stripping safe: plain const object, no enum). */
export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];
