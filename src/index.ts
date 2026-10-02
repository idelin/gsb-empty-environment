export {
  CodecError,
  TruncatedMessageError,
  MalformedMessageError,
  ValidationError,
} from './errors.ts';
export {
  encodeVarint,
  encodeVarintSigned,
  decodeVarint,
  zigZagEncode32,
  zigZagDecode32,
  zigZagEncode64,
  zigZagDecode64,
  MAX_VARINT_BYTES,
} from './varint.ts';
export { WireReader, WireType, type Tag } from './reader.ts';
export { WireWriter } from './writer.ts';
export {
  decodeMessage,
  encodeMessage,
  validateMessage,
  DEFAULT_MAX_DEPTH,
  type FieldSpec,
  type MessageSchema,
  type ScalarType,
  type DecodeOptions,
} from './schema.ts';
export {
  AdaptiveLoadController,
  DisposedError,
  AcquireTimeoutError,
  type AdaptiveOptions,
  type Adjustment,
  type LoadSample,
  type Permit,
  type ScoreWeights,
} from './adaptive.ts';
