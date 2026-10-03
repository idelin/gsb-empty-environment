export { ProtobufDecodeError, type DecodeErrorCode } from './errors.ts';
export {
  MAX_VARINT_BYTES,
  decodeVarint,
  encodeVarint,
  encodeVarintInto,
  varintLength,
  zigzagDecode32,
  zigzagDecode64,
  zigzagEncode32,
  zigzagEncode64,
} from './varint.ts';
export { WireReader, WireType, MAX_FIELD_NUMBER, type Tag, type WireTypeValue } from './reader.ts';
export { WireWriter } from './writer.ts';
export {
  defineSchema,
  expectedWireType,
  isPackable,
  resolveMessage,
  type FieldDescriptor,
  type FieldKind,
  type MessageRef,
  type MessageSchema,
} from './schema.ts';
export {
  DEFAULT_MAX_DEPTH,
  decodeMessage,
  encodeMessage,
  type DecodeOptions,
  type MessageObject,
} from './message.ts';
