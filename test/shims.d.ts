// Minimal ambient declarations so the project compiles with "types": []
// (an unrelated legacy @types/node higher up the tree must not leak in).
declare module 'assert' {
  interface Assert {
    (value: unknown, message?: string): asserts value;
    strictEqual(actual: unknown, expected: unknown, message?: string): void;
    notStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    deepStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): asserts value;
    throws(block: () => unknown, expected?: unknown, message?: string): void;
  }
  const assert: Assert;
  export = assert;
}

declare const process: {
  stdout: { write(chunk: string): void };
  stderr: { write(chunk: string): void };
  exitCode: number;
};
