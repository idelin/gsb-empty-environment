// Minimal Node shims (the sandbox has no installable @types/node).
declare module 'assert' {
  function assert(value: unknown, message?: string): asserts value;
  namespace assert {
    function ok(value: unknown, message?: string): asserts value;
    function strictEqual(actual: unknown, expected: unknown, message?: string): void;
    function deepStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    function throws(fn: () => void, error?: RegExp | (new (...args: never[]) => Error), message?: string): void;
  }
  export = assert;
}

declare const process: {
  exit(code?: number): never;
};

declare const console: {
  log(...args: unknown[]): void;
};
