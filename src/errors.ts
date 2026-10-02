/** Base class for every error raised by the codec. */
export class CodecError extends Error {
  readonly offset: number;
  constructor(message: string, offset = -1) {
    super(message);
    this.name = new.target.name;
    this.offset = offset;
  }
}

/**
 * Raised when the buffer ends before the current value/field is complete.
 * Carries enough context for callers to pinpoint the truncated field.
 */
export class TruncationError extends CodecError {
  readonly needed: number;
  readonly available: number;
  readonly fieldPath: string;
  constructor(
    what: string,
    needed: number,
    available: number,
    offset: number,
    fieldPath = "",
  ) {
    super(
      `truncated ${what}: need ${needed} byte(s), only ${available} left` +
        (fieldPath ? ` (field ${fieldPath})` : "") +
        ` at offset ${offset}`,
      offset,
    );
    this.needed = needed;
    this.available = available;
    this.fieldPath = fieldPath;
  }
}

/** Raised when bytes are present but violate the wire format. */
export class MalformedError extends CodecError {}

/** Raised when a decoded/encoded value violates the reflection schema. */
export class SchemaValidationError extends CodecError {
  readonly issues: readonly string[];
  constructor(issues: readonly string[]) {
    super(`schema validation failed:\n  - ${issues.join("\n  - ")}`);
    this.issues = issues;
  }
}
