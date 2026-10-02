/** Error hierarchy for the protobuf wire-format codec. */

export class ProtobufTruncationError extends Error {
  readonly offset: number;
  readonly needed: number;
  readonly available: number;

  constructor(offset: number, needed: number, available: number) {
    super(
      `truncated buffer at offset ${offset}: needed ${needed} byte(s), only ${available} available`
    );
    this.name = 'ProtobufTruncationError';
    this.offset = offset;
    this.needed = needed;
    this.available = available;
  }
}

export class ProtobufWireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ProtobufWireError';
  }
}

export class ProtobufValidationError extends Error {
  readonly field: string;

  constructor(field: string, message: string) {
    super(`field "${field}": ${message}`);
    this.name = 'ProtobufValidationError';
    this.field = field;
  }
}
