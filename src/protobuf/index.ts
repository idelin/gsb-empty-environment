export {
  ProtobufTruncationError,
  ProtobufValidationError,
  ProtobufWireError,
} from './errors.ts';
export { encodeVarint, decodeVarint, MAX_VARINT_BYTES } from './varint.ts';
export {
  zigzagEncode32,
  zigzagDecode32,
  zigzagEncode64,
  zigzagDecode64,
} from './zigzag.ts';
export { WireType, makeTag, parseTag } from './wire.ts';
export { Reader } from './reader.ts';
export { Writer } from './writer.ts';
export {
  encodeMessage,
  decodeMessage,
} from './schema.ts';
export type {
  ScalarType,
  FieldType,
  FieldDescriptor,
  MessageDescriptor,
} from './schema.ts';
