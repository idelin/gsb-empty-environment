import { formatPath, type JsonPath } from './json.ts';

/**
 * A failure raised while transforming one record at one pipeline step.
 * Always carries enough context to locate the problem: which record,
 * which step, and the exact path inside the JSON tree.
 */
export class TransformError extends Error {
  readonly recordId: string;
  readonly step: string;
  readonly path: JsonPath;
  readonly cause?: unknown;

  constructor(init: {
    recordId: string;
    step: string;
    path: JsonPath;
    message: string;
    cause?: unknown;
  }) {
    super(
      `record ${JSON.stringify(init.recordId)} failed at step ${JSON.stringify(init.step)} ` +
        `path ${formatPath(init.path)}: ${init.message}`,
    );
    this.name = 'TransformError';
    this.recordId = init.recordId;
    this.step = init.step;
    this.path = init.path;
    this.cause = init.cause;
  }

  /** The formatted JSON path, e.g. `$.items[2].name`. */
  get pathString(): string {
    return formatPath(this.path);
  }

  toJSON(): Record<string, unknown> {
    return {
      recordId: this.recordId,
      step: this.step,
      path: this.pathString,
      message: this.message,
    };
  }
}

/**
 * Thrown by step code to flag a bad value at a specific path. The pipeline
 * wraps it into a {@link TransformError} with record id and step name.
 */
export class RecordError extends Error {
  readonly path: JsonPath;

  constructor(path: JsonPath, message: string) {
    super(message);
    this.name = 'RecordError';
    this.path = path;
  }
}

/** Convenience for step authors: `failAt(['items', 2, 'name'], 'must be a string')`. */
export function failAt(path: JsonPath, message: string): never {
  throw new RecordError(path, message);
}
