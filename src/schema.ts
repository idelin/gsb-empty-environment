import { WireType } from "./wire.ts";

/** Scalar field kinds understood by the reflection engine. */
export type ScalarType =
  | "int32"
  | "int64"
  | "uint32"
  | "uint64"
  | "sint32"
  | "sint64"
  | "bool"
  | "enum"
  | "fixed32"
  | "fixed64"
  | "sfixed32"
  | "sfixed64"
  | "float"
  | "double"
  | "string"
  | "bytes";

export type FieldType = ScalarType | "message";

export interface FieldDescriptor {
  /** Protobuf field number (>= 1). */
  readonly no: number;
  readonly name: string;
  readonly type: FieldType;
  /** Sub-schema, required when `type === "message"`. */
  readonly message?: MessageSchema;
  readonly repeated?: boolean;
  /** Pack scalar repeated fields into one length-delimited block. */
  readonly packed?: boolean;
  readonly required?: boolean;
}

export interface MessageSchema {
  readonly name: string;
  readonly fields: readonly FieldDescriptor[];
}

/** Wire type a scalar field uses on the wire. */
export function wireTypeOf(type: FieldType): WireType {
  switch (type) {
    case "int32":
    case "int64":
    case "uint32":
    case "uint64":
    case "sint32":
    case "sint64":
    case "bool":
    case "enum":
      return WireType.Varint;
    case "fixed64":
    case "sfixed64":
    case "double":
      return WireType.Fixed64;
    case "fixed32":
    case "sfixed32":
    case "float":
      return WireType.Fixed32;
    case "string":
    case "bytes":
    case "message":
      return WireType.LengthDelimited;
  }
}

/** Types that may legally appear inside a packed repeated block. */
export function isPackable(type: FieldType): boolean {
  return type !== "string" && type !== "bytes" && type !== "message";
}

/** Decoded message: plain object keyed by field name. */
export type DecodedMessage = Record<string, unknown> & {
  /** Fields present on the wire but absent from the schema. */
  $unknown?: Record<number, unknown[]>;
};
