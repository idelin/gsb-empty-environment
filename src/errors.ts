/** Base class for all codec errors. */
export class CodecError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(message);
    this.name = new.target.name;
    this.offset = offset;
  }
}

/**
 * Raised when a read would cross the end of the buffer.
 * This is the safe out-of-bounds guard: every cursor advance is checked.
 */
export class TruncatedMessageError extends CodecError {
  readonly needed: number;
  readonly remaining: number;
  constructor(offset: number, needed: number, remaining: number, what: string) {
    super(
      `truncated ${what} at offset ${offset}: need ${needed} byte(s), only ${remaining} remaining`,
      offset,
    );
    this.needed = needed;
    this.remaining = remaining;
  }
}

/** Raised when bytes are well-framed but semantically invalid. */
export class MalformedMessageError extends CodecError {}

/** Raised by the reflection validation engine. Carries every issue found. */
export class ValidationError extends Error {
  readonly issues: readonly string[];
  constructor(issues: string[]) {
    super(`validation failed with ${issues.length} issue(s):\n- ${issues.join('\n- ')}`);
    this.name = 'ValidationError';
    this.issues = issues;
  }
}
