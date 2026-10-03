declare module 'node:test' {
  export function test(name: string, fn: () => void | Promise<void>): void;
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: () => void | Promise<void>): void;
}

declare module 'node:assert/strict' {
  interface Assert {
    (value: unknown, message?: string): void;
    equal(actual: unknown, expected: unknown, message?: string): void;
    strictEqual(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    deepStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): void;
    throws(fn: () => unknown, expected?: unknown, message?: string): void;
    rejects(fn: Promise<unknown> | (() => Promise<unknown>), expected?: unknown, message?: string): Promise<void>;
    fail(message?: string): never;
  }
  const assert: Assert;
  export default assert;
}

declare module 'node:timers/promises' {
  export function setTimeout(ms: number): Promise<void>;
}

declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};

declare const performance: {
  now(): number;
};
