/** Error taxonomy shared by the wire codec, schema validator and arbiter. */

export class ProtobufDecodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ProtobufDecodeError';
  }
}

/** Raised whenever a read would cross the end of the input buffer. */
export class TruncatedMessageError extends ProtobufDecodeError {
  readonly needed: number;
  readonly available: number;
  constructor(context: string, needed: number, available: number) {
    super(
      `truncated message while reading ${context}: ` +
        `needed ${needed} byte(s), only ${available} available`,
    );
    this.name = 'TruncatedMessageError';
    this.needed = needed;
    this.available = available;
  }
}

export interface ValidationIssue {
  path: string;
  message: string;
}

export class ValidationError extends Error {
  readonly issues: ValidationIssue[];
  constructor(issues: ValidationIssue[]) {
    super(
      `schema validation failed with ${issues.length} issue(s): ` +
        issues.map((i) => `${i.path}: ${i.message}`).join('; '),
    );
    this.name = 'ValidationError';
    this.issues = issues;
  }
}

/** Raised by the arbiter when a compare-and-swap loses a contention round. */
export class ConflictError extends Error {
  readonly key: string;
  readonly expectedVersion: number;
  readonly currentVersion: number;
  constructor(key: string, expectedVersion: number, currentVersion: number) {
    super(
      `conflict on key "${key}": expected version ${expectedVersion}, ` +
        `current version is ${currentVersion}`,
    );
    this.name = 'ConflictError';
    this.key = key;
    this.expectedVersion = expectedVersion;
    this.currentVersion = currentVersion;
  }
}
