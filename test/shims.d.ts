// Minimal Node.js shims so the tests compile without @types/node
// (the sandbox has no network access to install it).
declare const process: {
  stdout: { write(chunk: string): void };
  exit(code?: number): never;
};

declare module 'assert' {
  function assert(value: unknown, message?: string): asserts value;
  namespace assert {
    function strictEqual(actual: unknown, expected: unknown, message?: string): void;
    function deepStrictEqual(actual: unknown, expected: unknown, message?: string): void;
    function throws(fn: () => void, message?: string): void;
    function doesNotThrow(fn: () => void, message?: string): void;
  }
  export = assert;
}
