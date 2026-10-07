/** A type error raised while evaluating an expression against a row. */
export class EvalError extends Error {
  /** Path from the expression root to the node that failed, e.g. ['left', 'right']. */
  readonly path: readonly string[];

  constructor(path: readonly string[], message: string) {
    super(`${message} (at ${formatPath(path)})`);
    this.name = 'EvalError';
    this.path = Object.freeze([...path]);
  }
}

export const formatPath = (path: readonly string[]): string =>
  path.length === 0 ? '$' : `$.${path.join('.')}`;
