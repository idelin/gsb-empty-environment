/** Error thrown on any malformed / truncated wire-format input. */
export class DecodeError extends Error {
  readonly offset: number;
  readonly path: string;

  constructor(message: string, offset: number, path = "") {
    super(
      path
        ? `${message} (field path: ${path}, byte offset: ${offset})`
        : `${message} (byte offset: ${offset})`,
    );
    this.name = "DecodeError";
    this.offset = offset;
    this.path = path;
  }
}

/** Error thrown when a value does not satisfy its reflection schema. */
export class ValidationError extends Error {
  readonly violations: string[];

  constructor(violations: string[]) {
    super(`schema validation failed:\n  - ${violations.join("\n  - ")}`);
    this.name = "ValidationError";
    this.violations = violations;
  }
}
