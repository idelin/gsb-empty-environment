// Minimal ambient declarations for the Node built-ins used by this package.
// The runtime image ships without @types/node and without network access, so the
// exact subset of node:test / node:assert / timers needed here is declared locally.

declare module "node:test" {
  export type TestFn = (t: unknown) => void | Promise<void>;
  export function test(name: string, fn: TestFn): void;
}

declare module "node:assert/strict" {
  interface AssertStrict {
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    notEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): void;
    throws(fn: () => unknown, error?: RegExp | typeof Error): void;
  }
  const assert: AssertStrict;
  export default assert;
}

declare function setTimeout(
  handler: (...args: never[]) => void,
  timeoutMs?: number,
): unknown;
declare function clearTimeout(handle: unknown): void;
