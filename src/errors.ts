import type { SourceLocation } from './location.ts';

export type TemplateErrorCode =
  | 'TEMPLATE_ERROR'
  | 'SYNTAX'
  | 'FORBIDDEN_ACCESS'
  | 'UNDECLARED_VARIABLE'
  | 'MISSING_VARIABLE'
  | 'UNKNOWN_FILTER'
  | 'BUDGET_EXCEEDED'
  | 'UNSAFE_VALUE';

/**
 * A deliberately small error surface. Messages contain only a template
 * position; values, source snippets, environment data and filter failures are
 * never copied into the error.
 */
export class TemplateError extends Error {
  readonly code: TemplateErrorCode;
  readonly offset: number;
  readonly line: number;
  readonly column: number;

  constructor(location: SourceLocation, code: TemplateErrorCode = 'TEMPLATE_ERROR') {
    super(`Template error at line ${location.line}, column ${location.column}`);
    this.name = 'TemplateError';
    this.code = code;
    this.offset = location.offset;
    this.line = location.line;
    this.column = location.column;
  }
}
