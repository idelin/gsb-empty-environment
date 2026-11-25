declare module 'node:test' {
  export type TestContext = unknown;
  export type TestImplementation = (context: TestContext) => void | Promise<void>;
  export interface TestRunner {
    (name: string, implementation?: TestImplementation): void;
    (implementation?: TestImplementation): void;
  }
  const test: TestRunner;
  export default test;
}

declare module 'node:assert/strict' {
  const assert: {
    (value: unknown, message?: string): asserts value;
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): asserts value;
    fail(message?: string | Error): never;
    throws(fn: () => unknown, message?: string | Error): void;
  };
  export default assert;
}
