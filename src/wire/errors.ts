export class DecodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DecodeError';
  }
}

export class TruncatedFieldError extends DecodeError {
  readonly offset: number;
  readonly needed: number;
  readonly available: number;

  constructor(what: string, offset: number, needed: number, available: number) {
    super(
      `truncated ${what}: need ${needed} byte(s) at offset ${offset}, only ${available} available`,
    );
    this.name = 'TruncatedFieldError';
    this.offset = offset;
    this.needed = needed;
    this.available = available;
  }
}

export class MalformedVarintError extends DecodeError {
  constructor(message: string) {
    super(message);
    this.name = 'MalformedVarintError';
  }
}

export class ValidationError extends Error {
  readonly issues: readonly { path: string; message: string }[];

  constructor(issues: readonly { path: string; message: string }[]) {
    super(
      `validation failed with ${issues.length} issue(s): ` +
        issues.map((i) => `${i.path}: ${i.message}`).join('; '),
    );
    this.name = 'ValidationError';
    this.issues = issues;
  }
}
