/**
 * Use Bun's runner when the suite is executed with `bun test`, otherwise use
 * Node's built-in test runner (`node --experimental-strip-types --test`).
 */
const isBun = typeof (globalThis as { Bun?: unknown }).Bun !== "undefined";

const module = isBun
  ? await import("bun:test")
  : await import("node:test");

export const test: typeof import("node:test").test = module.test;
