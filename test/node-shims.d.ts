/**
 * Minimal ambient declarations for the Node test runner.
 *
 * The sandbox has no network access to install `@types/node`; these shims
 * cover only the surface used by the tests so the project still type-checks
 * under `strict`. Runtime behaviour comes from Node itself.
 */

declare module 'node:test' {
  export interface TestContext {
    name: string;
  }
  export function test(name: string, fn: (context: TestContext) => void | Promise<void>): void;
}

declare module 'node:assert/strict' {
  interface AssertStrict {
    (value: unknown, message?: string | Error): asserts value;
    ok(value: unknown, message?: string | Error): asserts value;
    equal(actual: unknown, expected: unknown, message?: string | Error): void;
    deepEqual(actual: unknown, expected: unknown, message?: string | Error): void;
    fail(message?: string | Error): never;
    throws(fn: () => unknown, error?: unknown, message?: string | Error): void;
  }
  const assert: AssertStrict;
  export default assert;
}
