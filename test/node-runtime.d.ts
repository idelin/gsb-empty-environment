declare module "node:assert/strict" {
  const assert: any;
  export default assert;
}

declare module "node:test" {
  const test: (
    name: string,
    callback: () => void | Promise<void>,
  ) => void;
  export default test;
}
