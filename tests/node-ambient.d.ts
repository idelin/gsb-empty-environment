/**
 * Minimal ambient declarations for the Node built-in test runner and assert
 * module. Keeps the library zero-dependency; the real implementations are
 * provided by the Node runtime at execution time.
 */
declare module 'node:test' {
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: () => void | Promise<void>): void;
}

declare module 'node:assert/strict' {
  interface AssertStrict {
    (value: unknown, message?: string | Error): asserts value;
    deepEqual(actual: unknown, expected: unknown, message?: string | Error): void;
    equal(actual: unknown, expected: unknown, message?: string | Error): void;
    ok(value: unknown, message?: string | Error): asserts value;
    fail(message?: string | Error): never;
    throws(fn: () => unknown, errorMatcher?: unknown, message?: string | Error): void;
  }
  const assert: AssertStrict;
  export default assert;
}
