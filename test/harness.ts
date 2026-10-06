import { FailureReason, OpId, State } from '../src/operation';

type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function assert(condition: boolean, message: string): void {
  if (!condition) throw new Error(`assertion failed: ${message}`);
}

export function assertEqual<T>(actual: T, expected: T, message: string): void {
  if (actual !== expected) {
    throw new Error(
      `${message}: expected ${String(expected)}, got ${String(actual)}`,
    );
  }
}

export function assertStateEqual(
  actual: State,
  expected: ReadonlyMap<string, number>,
  message: string,
): void {
  const a = [...actual.entries()].sort(([x], [y]) => (x < y ? -1 : 1));
  const e = [...expected.entries()].sort(([x], [y]) => (x < y ? -1 : 1));
  assert(
    a.length === e.length && a.every(([k, v], i) => k === e[i]![0] && v === e[i]![1]),
    `${message}: state mismatch\n  actual:   ${JSON.stringify(a)}\n  expected: ${JSON.stringify(e)}`,
  );
}

export function assertFailuresEqual(
  actual: ReadonlyMap<OpId, FailureReason>,
  expected: ReadonlyMap<OpId, FailureReason>,
  message: string,
): void {
  const a = [...actual.entries()].sort(([x], [y]) => (x < y ? -1 : 1));
  const e = [...expected.entries()].sort(([x], [y]) => (x < y ? -1 : 1));
  assert(
    a.length === e.length && a.every(([k, v], i) => k === e[i]![0] && v === e[i]![1]),
    `${message}: failures mismatch\n  actual:   ${JSON.stringify(a)}\n  expected: ${JSON.stringify(e)}`,
  );
}

export function run(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log(`ok - ${name}`);
    } catch (err) {
      failed += 1;
      console.error(`FAIL - ${name}`);
      console.error(err instanceof Error ? err.message : err);
    }
  }
  console.log(`${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exit(1);
}
