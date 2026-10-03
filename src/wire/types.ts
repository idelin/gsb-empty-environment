export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;

export type WireType = (typeof WireType)[keyof typeof WireType];

export function wireTypeName(wireType: number): string {
  switch (wireType) {
    case WireType.Varint:
      return 'Varint';
    case WireType.Fixed64:
      return 'Fixed64';
    case WireType.LengthDelimited:
      return 'LengthDelimited';
    case WireType.Fixed32:
      return 'Fixed32';
    default:
      return `Unknown(${wireType})`;
  }
}

export function isValidWireType(wireType: number): wireType is WireType {
  return (
    wireType === WireType.Varint ||
    wireType === WireType.Fixed64 ||
    wireType === WireType.LengthDelimited ||
    wireType === WireType.Fixed32
  );
}
