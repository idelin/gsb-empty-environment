/** Error thrown when decoding fails. Carries the byte offset where it happened. */
export class DecodeError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (at offset ${offset})`);
    this.name = 'DecodeError';
    this.offset = offset;
  }
}

/** Error thrown when encoding fails (invalid value for the target wire type). */
export class EncodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'EncodeError';
  }
}

/** Error thrown by the reflection schema validator. Carries the field path. */
export class ValidationError extends Error {
  readonly path: readonly string[];
  constructor(message: string, path: readonly string[]) {
    super(`${message} (at ${path.join('.') || '<root>'})`);
    this.name = 'ValidationError';
    this.path = path;
  }
}
