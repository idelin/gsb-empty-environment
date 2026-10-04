// Minimal ambient declarations (offline environment, no @types/node).
declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};

declare module 'node:assert' {
  interface Assert {
    (value: unknown, message?: string): asserts value;
    ok(value: unknown, message?: string): void;
    strictEqual(actual: unknown, expected: unknown, message?: string): void;
    notStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    deepStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    throws(fn: () => unknown, pattern?: RegExp): void;
    match(actual: string, pattern: RegExp, message?: string): void;
  }
  const assert: Assert;
  export = assert;
}
