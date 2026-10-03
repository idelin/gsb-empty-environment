// 本仓库不依赖 @types/node（离线环境）。这里只声明代码用到的最小 Node.js API 类型。
// 运行时均使用 Node 内置模块的真实实现。

declare var console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};

declare var process: {
  exitCode: number | undefined;
  argv: string[];
  env: Record<string, string | undefined>;
};

declare module "node:test" {
  export type TestFn = () => void | Promise<void>;
  export function test(name: string, fn: TestFn): void;
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: TestFn): void;
}

declare module "node:assert" {
  export function ok(value: unknown, message?: string): void;
  export function equal(actual: unknown, expected: unknown, message?: string): void;
  export function strictEqual(actual: unknown, expected: unknown, message?: string): void;
  export function notStrictEqual(actual: unknown, expected: unknown, message?: string): void;
  export function deepStrictEqual(actual: unknown, expected: unknown, message?: string): void;
  export function fail(message?: string): never;
}
