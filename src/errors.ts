/**
 * Error hierarchy. Every error message references the *user-supplied* path
 * expression (and optionally a character position inside it). Internal object
 * graphs, host values and engine internals are never interpolated into
 * messages, so callers can safely surface these errors to end users.
 */
export class PathError extends Error {
  readonly expression: string;
  readonly position: number | undefined;

  constructor(kind: string, message: string, expression: string, position?: number) {
    const where = position !== undefined ? `, position=${position}` : '';
    super(`${kind}: ${message} [expression=${JSON.stringify(expression)}${where}]`);
    this.name = kind;
    this.expression = expression;
    this.position = position;
  }
}

/** Raised while parsing/compiling an expression (syntax, depth, denied keys). */
export class ParseError extends PathError {
  constructor(message: string, expression: string, position?: number) {
    super('ParseError', message, expression, position);
  }
}

/** Raised when evaluation touches a denied property (defense in depth). */
export class AccessDeniedError extends PathError {
  constructor(message: string, expression: string, position?: number) {
    super('AccessDeniedError', message, expression, position);
  }
}

/** Raised when the shared execution-step budget is exhausted. */
export class BudgetExceededError extends PathError {
  constructor(message: string, expression: string) {
    super('BudgetExceededError', message, expression);
  }
}
