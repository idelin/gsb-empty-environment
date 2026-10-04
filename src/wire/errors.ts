/** Error thrown when the wire-format decoder encounters malformed or truncated input. */
export class ProtobufDecodeError extends Error {
  readonly offset: number;
  readonly reason: string;

  constructor(reason: string, offset: number) {
    super(`protobuf decode error at offset ${offset}: ${reason}`);
    this.name = 'ProtobufDecodeError';
    this.offset = offset;
    this.reason = reason;
  }
}

/** Error thrown when a decoded message fails schema (reflection) validation. */
export class SchemaValidationError extends Error {
  readonly issues: string[];

  constructor(messageName: string, issues: string[]) {
    super(`schema validation failed for ${messageName}: ${issues.join('; ')}`);
    this.name = 'SchemaValidationError';
    this.issues = issues;
  }
}
