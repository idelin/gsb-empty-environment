/** Minimal test harness (node:test is unavailable on this Node version). */

type TestFn = () => void;

const tests: { name: string; fn: TestFn }[] = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

export function assertEq(actual: unknown, expected: unknown, msg = ""): void {
  const a = String(actual);
  const e = String(expected);
  if (a !== e)
    throw new Error(`assertion failed: expected ${e}, got ${a} ${msg}`);
}

export function assertThrows(fn: () => void, msg = ""): void {
  let threw = false;
  try {
    fn();
  } catch {
    threw = true;
  }
  if (!threw) throw new Error(`assertion failed: expected throw ${msg}`);
}

export function runAll(): void {
  let passed = 0;
  const failures: { name: string; err: unknown }[] = [];
  for (const t of tests) {
    try {
      t.fn();
      passed++;
      console.log(`ok   ${t.name}`);
    } catch (err) {
      failures.push({ name: t.name, err });
      console.log(`FAIL ${t.name}`);
      console.log(`     ${err instanceof Error ? err.message : err}`);
    }
  }
  console.log(`\n${passed}/${tests.length} tests passed`);
  if (failures.length > 0) process.exit(1);
}
