declare module "node:test" {
  type TestContext = unknown;
  type TestFn = (t?: TestContext) => void | Promise<void>;
  export function test(name: string, fn: TestFn): void;
  export function test(name: string, opts: { timeout?: number }, fn: TestFn): void;
  export default test;
}
