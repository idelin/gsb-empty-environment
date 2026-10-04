// Minimal Node.js type shims (offline environment, no @types/node).
// Only the surface used by this project is declared.

declare class Buffer extends Uint8Array {
  static alloc(size: number, fill?: number): Buffer;
  static allocUnsafe(size: number): Buffer;
  static from(data: Uint8Array | readonly number[]): Buffer;
  static from(data: string, encoding?: string): Buffer;
  static concat(list: readonly Buffer[], totalLength?: number): Buffer;
  static byteLength(data: string, encoding?: string): number;
  static isBuffer(obj: unknown): obj is Buffer;
  subarray(begin?: number, end?: number): Buffer;
  slice(begin?: number, end?: number): Buffer;
  copy(target: Buffer, targetStart?: number, sourceStart?: number, sourceEnd?: number): number;
  writeUInt8(value: number, offset: number): number;
  writeUInt32LE(value: number, offset: number): number;
  writeUInt32BE(value: number, offset: number): number;
  writeBigUInt64LE(value: bigint, offset: number): number;
  readUInt8(offset: number): number;
  readUInt32LE(offset: number): number;
  readUInt32BE(offset: number): number;
  readBigUInt64LE(offset: number): bigint;
  write(data: string, encoding?: string): number;
  toString(encoding?: string): string;
  toString(encoding: string, start?: number, end?: number): string;
  equals(other: Buffer): boolean;
  fill(value: number): this;
}

declare var console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
  warn(...args: unknown[]): void;
};

declare var process: {
  argv: string[];
  env: Record<string, string | undefined>;
  exitCode?: number;
  cwd(): string;
  hrtime(): [number, number];
  memoryUsage(): { rss: number; heapUsed: number; heapTotal: number };
};

declare function setTimeout(fn: () => void, ms?: number): unknown;

declare module "fs" {
  export function existsSync(path: string): boolean;
  export function mkdirSync(path: string, opts?: { recursive?: boolean }): void;
  export function rmSync(path: string, opts?: { recursive?: boolean; force?: boolean }): void;
  export function unlinkSync(path: string): void;
  export function readFileSync(path: string): Buffer;
  export function writeFileSync(path: string, data: Buffer | string): void;
  export function openSync(path: string, flags: string): number;
  export function closeSync(fd: number): void;
  export function fstatSync(fd: number): { size: number };
  export function readSync(fd: number, buffer: Buffer, offset: number, length: number, position: number | null): number;
  export function writeSync(fd: number, buffer: Buffer, offset?: number, length?: number, position?: number | null): number;
  export function fsyncSync(fd: number): void;
}

declare module "path" {
  export function join(...parts: string[]): string;
  export function resolve(...parts: string[]): string;
}

declare module "node:test" {
  type TestFn = () => void | Promise<void>;
  function test(name: string, fn: TestFn): void;
  function test(name: string, opts: { concurrency?: number }, fn: TestFn): void;
  namespace test {
    function only(name: string, fn: TestFn): void;
    function skip(name: string, fn: TestFn): void;
  }
  export function before(fn: TestFn): void;
  export function after(fn: TestFn): void;
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: TestFn): void;
  export default test;
}

declare module "node:assert/strict" {
  export function ok(value: unknown, message?: string): asserts value;
  export function equal(actual: unknown, expected: unknown, message?: string): void;
  export function notEqual(actual: unknown, expected: unknown, message?: string): void;
  export function deepEqual(actual: unknown, expected: unknown, message?: string): void;
  export function notDeepEqual(actual: unknown, expected: unknown, message?: string): void;
  export function throws(fn: () => void, expected?: RegExp | (new (...args: never[]) => Error) | ((err: unknown) => boolean), message?: string): void;
  export function rejects(fn: () => Promise<unknown>, expected?: RegExp): Promise<void>;
  export function fail(message?: string): never;
  export function match(actual: string, expected: RegExp): void;
}

declare module "fs" {
  export function ftruncateSync(fd: number, len: number): void;
}

declare module "fs" {
  export function mkdtempSync(prefix: string): string;
  export function appendFileSync(path: string, data: Buffer): void;
}

declare module "os" {
  export function tmpdir(): string;
}
