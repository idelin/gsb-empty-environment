declare module 'node:crypto' {
  export function randomInt(min: number, max: number): number;
  export function sign(algorithm: unknown, data: Uint8Array, key: unknown): {
    toString(encoding: string): string;
  };
  export function verify(
    algorithm: unknown,
    data: Uint8Array,
    key: unknown,
    signature: Uint8Array,
  ): boolean;
  export function generateKeyPairSync(
    type: 'ed25519' | string,
  ): {
    publicKey: { export(options: { type: string; format: string }): string };
    privateKey: { export(options: { type: string; format: string }): string };
  };
  export function createHash(algorithm: string): {
    update(data: Uint8Array): { digest(encoding: 'hex'): string };
  };
}

declare module 'node:test' {
  type TestFn = (...args: any[]) => unknown | Promise<unknown>;
  const test: {
    (name: string, fn: TestFn): void;
    (fn: TestFn): void;
    only: (name: string, fn: TestFn) => void;
    skip: (name: string, fn: TestFn) => void;
  };
  export default test;
}

declare module 'node:assert/strict' {
  const assert: {
    (value: unknown, message?: string): asserts value;
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): asserts value;
    rejects(fn: () => Promise<unknown>, error?: unknown): Promise<void>;
    throws(fn: () => unknown, error?: unknown): void;
  };
  export default assert;
}
