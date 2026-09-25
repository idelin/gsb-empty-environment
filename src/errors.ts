/** Error codes used across the library, grouped by phase. */
export type ErrorCode =
  | 'COMPILE_CYCLE' // contradictory conversion cycle in the unit graph
  | 'COMPILE_UNIT' // unknown unit, dimension mismatch, zero ratio, ...
  | 'COMPILE_PATH' // invalid / duplicate / nested-conflicting path patterns
  | 'NO_CONVERSION' // no conversion path between two units at runtime
  | 'INPUT_NOT_JSON' // input is not a JSON-compatible tree
  | 'PATH_TYPE' // tree shape does not match a rule's path pattern
  | 'VALUE_NOT_NUMERIC' // target field is not a number / numeric string
  | 'UNSAFE_INTEGER' // result does not fit the safe-integer range
  | 'CONSTRAINT'; // cross-field constraint failed on the migrated object

/**
 * Every error carries a machine-readable `code` and, whenever the problem
 * can be located in the input tree, a concrete JSON `path`
 * (e.g. "$.sensors[2].tempC").
 */
export class UnitMigrationError extends Error {
  readonly code: ErrorCode;
  readonly path?: string;
  readonly details?: unknown;

  constructor(code: ErrorCode, message: string, opts?: { path?: string; details?: unknown }) {
    super(message);
    this.name = 'UnitMigrationError';
    this.code = code;
    if (opts?.path !== undefined) this.path = opts.path;
    if (opts?.details !== undefined) this.details = opts.details;
  }
}
