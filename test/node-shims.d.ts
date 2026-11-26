declare module "node:test" {
  export type TestContext = unknown;
  const test: {
    (name: string, fn: () => void | Promise<void>): void;
    skip(name: string, fn: () => void | Promise<void>): void;
  };
  export default test;
}

declare module "node:assert/strict" {
  const assert: {
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): asserts value;
    throws(fn: () => unknown, predicate?: (error: unknown) => boolean): void;
  };
  export default assert;
}
