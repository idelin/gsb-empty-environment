declare module 'node:test' {
  export function test(name: string, fn: () => void | Promise<void>): void;
  export function describe(name: string, fn: () => void): void;
  export function before(fn: () => void | Promise<void>): void;
  export function after(fn: () => void | Promise<void>): void;
  export function beforeEach(fn: () => void | Promise<void>): void;
  export function afterEach(fn: () => void | Promise<void>): void;
}
declare module 'node:assert/strict' {
  interface AssertStrict {
    (value: unknown, message?: string | Error): void;
    equal(actual: unknown, expected: unknown, message?: string | Error): void;
    deepEqual(actual: unknown, expected: unknown, message?: string | Error): void;
    ok(value: unknown, message?: string | Error): void;
    throws(fn: () => unknown, error?: unknown, message?: string | Error): void;
    rejects(fn: () => Promise<unknown>, error?: unknown, message?: string | Error): Promise<void>;
    match(actual: string, regexp: RegExp, message?: string | Error): void;
    notEqual(actual: unknown, expected: unknown, message?: string | Error): void;
    fail(message?: string | Error): never;
  }
  const assert: AssertStrict;
  export default assert;
}
