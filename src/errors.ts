/**
 * Every failure raised by this library is a PathError. The message and the
 * `userPath` field only ever reference the user-supplied path expression and
 * user-supplied key names -- never internal objects, values, or stack frames.
 */
export type ErrorCode =
  | 'PARSE_ERROR'
  | 'DEPTH_LIMIT'
  | 'FORBIDDEN_PROPERTY'
  | 'BUDGET_EXCEEDED'
  | 'HOST_OBJECT'
  | 'TYPE_ERROR';

export class PathError extends Error {
  readonly code: ErrorCode;
  readonly userPath: string;

  constructor(code: ErrorCode, userPath: string, detail: string) {
    super(`[${code}] ${detail} (in path ${JSON.stringify(userPath)})`);
    this.name = 'PathError';
    this.code = code;
    this.userPath = userPath;
  }
}
