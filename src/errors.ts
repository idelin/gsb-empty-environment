/** Errors always carry a machine-readable code and, when applicable, a path. */

export type ErrorCode =
  | 'unknown-path'
  | 'path-type-mismatch'
  | 'conflicting-rules'
  | 'contradictory-cycle'
  | 'dimension-mismatch'
  | 'kind-mismatch'
  | 'missing-rounding-mode'
  | 'value-type-mismatch'
  | 'constraint-violation'
  | 'unknown-unit';

export class MigrationError extends Error {
  readonly code: ErrorCode;
  /** Concrete instance path (e.g. `readings[2].value`) or schema pattern. */
  readonly path?: string;
  /** Extra structured details (e.g. cycle units, constraint name). */
  readonly details?: Record<string, unknown>;

  constructor(code: ErrorCode, message: string, path?: string, details?: Record<string, unknown>) {
    super(path ? `${message} (at ${path})` : message);
    this.name = 'MigrationError';
    this.code = code;
    this.path = path;
    this.details = details;
  }
}
