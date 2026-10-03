export { WireWriter } from './writer.ts';
export { WireReader } from './reader.ts';
export { WireType } from './wire-type.ts';
export { encode, decode, validate } from './schema.ts';
export type { FieldSpec, Schema, MessageObject, ScalarType } from './schema.ts';
export {
  ProtoCodecError, TruncatedError, OverflowError, InvalidWireTypeError, ValidationError,
} from './errors.ts';
