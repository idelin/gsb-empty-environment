// Minimal ambient declarations so editors/tsc can type-check without
// installing @types/node (this project is runtime-dependency-free and
// executes directly on Node >= 22.6 type stripping).
declare module 'node:test' {
  export function test(name: string, fn: () => void | Promise<void>): void;
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: () => void | Promise<void>): void;
}

declare module 'node:assert/strict' {
  export function equal(actual: unknown, expected: unknown, message?: string): void;
  export function notEqual(actual: unknown, expected: unknown, message?: string): void;
  export function deepEqual(actual: unknown, expected: unknown, message?: string): void;
  export function ok(value: unknown, message?: string): void;
  const assert: {
    equal: typeof equal;
    notEqual: typeof notEqual;
    deepEqual: typeof deepEqual;
    ok: typeof ok;
  };
  export default assert;
}

declare module 'node:buffer' {
  export const Buffer: {
    byteLength(value: string, encoding?: string): number;
    from(value: string, encoding?: string): { length: number };
  };
}

declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
