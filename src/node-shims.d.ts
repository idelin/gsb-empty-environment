declare const process: { argv: string[]; exitCode: number | undefined };

declare module 'node:fs' {
  export function readFileSync(path: string, encoding: string): string;
}

declare module 'node:test' {
  export function test(name: string, fn: () => void): void;
}

declare module 'node:assert/strict' {
  const assert: {
    equal(a: unknown, b: unknown): void;
    deepEqual(a: unknown, b: unknown): void;
    ok(v: unknown): void;
  };
  export default assert;
}
