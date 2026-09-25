export type MigrationErrorCode =
  | "INVALID_JSON_TREE"
  | "SCHEMA_INPUT"
  | "SCHEMA_OUTPUT"
  | "STEP_APPLY"
  | "NO_PATH"
  | "AMBIGUOUS_PATH"
  | "CHECKPOINT_INPUT_MISMATCH"
  | "CHECKPOINT_PLAN_MISMATCH";

/**
 * Every failure carries a concrete JSON path (`path`) locating the problem
 * inside the document, plus the step (`step`) that produced it. For
 * graph-level failures the path refers to the route, e.g. "v1 -> v2 -> v3".
 */
export class MigrationError extends Error {
  readonly code: MigrationErrorCode;
  readonly path: string;
  readonly step?: string;
  readonly issues?: ReadonlyArray<{ path: string; message: string }>;

  constructor(
    code: MigrationErrorCode,
    message: string,
    options: {
      path?: string;
      step?: string;
      issues?: ReadonlyArray<{ path: string; message: string }>;
    } = {},
  ) {
    super(message);
    this.name = "MigrationError";
    this.code = code;
    this.path = options.path ?? "$";
    this.step = options.step;
    this.issues = options.issues;
  }

  describe(): string {
    const where = this.step ? `step "${this.step}" at ${this.path}` : this.path;
    return `[${this.code}] ${where}: ${this.message}`;
  }
}
