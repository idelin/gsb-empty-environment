/**
 * Structured SQL/JSON path errors.
 *
 * `code` is a stable symbolic identifier (safe to switch on in user code).
 * `sqlstate` carries a best-effort SQLSTATE class; the primary contract is
 * the symbolic `code`, since exact SQLSTATE assignment varies across engines.
 */
export type JsonPathErrorCode =
  | 'JSON_PATH_SYNTAX'
  | 'JSON_PATH_MEMBER_OF_NON_OBJECT'
  | 'JSON_PATH_MEMBER_NOT_FOUND'
  | 'JSON_PATH_SUBSCRIPT_OF_NON_ARRAY'
  | 'JSON_PATH_INDEX_OUT_OF_BOUNDS'
  | 'JSON_PATH_INDEX_NOT_INTEGER'
  | 'JSON_PATH_SINGLE_VALUE_EXPECTED'
  | 'JSON_PATH_BOOLEAN_EXPECTED'
  | 'JSON_PATH_NUMERIC_EXPECTED'
  | 'JSON_PATH_DIVISION_BY_ZERO'
  | 'JSON_PATH_TYPE_MISMATCH'
  | 'JSON_PATH_INVALID_REGEX'
  | 'JSON_PATH_UNKNOWN_VARIABLE'
  | 'JSON_PATH_LAST_OUTSIDE_SUBSCRIPT'
  | 'JSON_PATH_INTERNAL';

export class JsonPathError extends Error {
  readonly code: JsonPathErrorCode;
  readonly sqlstate: string;
  readonly position: number | null;

  constructor(
    code: JsonPathErrorCode,
    message: string,
    position: number | null = null,
    sqlstate = '22023',
  ) {
    super(position === null ? message : `${message} (at offset ${position})`);
    this.name = 'JsonPathError';
    this.code = code;
    this.sqlstate = sqlstate;
    this.position = position;
  }
}

/** Structural errors are suppressed (mapped to an empty sequence) in lax mode. */
const STRUCTURAL_CODES: ReadonlySet<JsonPathErrorCode> = new Set([
  'JSON_PATH_MEMBER_OF_NON_OBJECT',
  'JSON_PATH_MEMBER_NOT_FOUND',
  'JSON_PATH_SUBSCRIPT_OF_NON_ARRAY',
  'JSON_PATH_INDEX_OUT_OF_BOUNDS',
  'JSON_PATH_SINGLE_VALUE_EXPECTED',
  'JSON_PATH_TYPE_MISMATCH',
]);

export function isStructuralError(code: JsonPathErrorCode): boolean {
  return STRUCTURAL_CODES.has(code);
}
