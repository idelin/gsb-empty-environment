// Minimal offline stand-ins for @types/node (only what the tests use).
declare module 'assert' {
  export const strict: {
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): void;
    throws(fn: () => void, error?: unknown): void;
  };
}
declare const process: {
  exit(code?: number): never;
};
