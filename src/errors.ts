/** Error hierarchy for the wire-format engine. */

export class WireFormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

/** Raised when a read would pass the end of the buffer (safe bounds guard). */
export class TruncatedFieldError extends WireFormatError {
  readonly offset: number;
  readonly needed: number;
  readonly available: number;
  constructor(offset: number, needed: number, available: number, context = "") {
    super(
      `truncated field${context ? ` (${context})` : ""}: need ${needed} byte(s) ` +
        `at offset ${offset}, only ${available} available`,
    );
    this.offset = offset;
    this.needed = needed;
    this.available = available;
  }
}

/** Raised when a varint exceeds 10 bytes or never terminates. */
export class MalformedVarintError extends WireFormatError {
  constructor(offset: number) {
    super(`malformed varint at offset ${offset}: exceeds 10 bytes / unterminated`);
  }
}

/** Raised when the wire type on the wire does not match the schema. */
export class WireTypeMismatchError extends WireFormatError {
  constructor(field: number, expected: number, actual: number) {
    super(
      `field ${field}: wire type mismatch, expected ${expected}, got ${actual}`,
    );
  }
}

/** Raised when a varint does not fit into a safe JS number. */
export class VarintOverflowError extends WireFormatError {
  constructor() {
    super("varint exceeds Number.MAX_SAFE_INTEGER; use readVarint64 (bigint)");
  }
}
