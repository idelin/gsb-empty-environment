/** Base class for all codec failures. */
export class ProtoCodecError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (at byte offset ${offset})`);
    this.name = new.target.name;
    this.offset = offset;
  }
}

/** Input buffer ended before the current field was fully read. */
export class TruncatedError extends ProtoCodecError {}

/** A varint exceeded 64 bits / 10 bytes, or a value exceeds its type range. */
export class OverflowError extends ProtoCodecError {}

/** A tag carried an unknown or unsupported wire type. */
export class InvalidWireTypeError extends ProtoCodecError {}

/** Reflection-mode validation rejected a value before encoding. */
export class ValidationError extends ProtoCodecError {
  readonly path: string;
  constructor(message: string, path: string) {
    super(`validation failed at "${path}": ${message}`, 0);
    this.path = path;
  }
}
