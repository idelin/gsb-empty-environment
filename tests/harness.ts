/** Minimal zero-dependency test harness (no node:test types needed). */

interface TestCase {
  name: string;
  fn: () => void;
}

const cases: TestCase[] = [];

export function test(name: string, fn: () => void): void {
  cases.push({ name, fn });
}

export function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(`Assertion failed: ${message}`);
  }
}

export function assertEqual<T>(actual: T, expected: T, message: string): void {
  if (actual !== expected) {
    throw new Error(
      `${message}\n  expected: ${String(expected)}\n  actual:   ${String(actual)}`,
    );
  }
}

export function assertDeepEqual(
  actual: unknown,
  expected: unknown,
  message: string,
): void {
  const actualJson = JSON.stringify(actual);
  const expectedJson = JSON.stringify(expected);
  if (actualJson !== expectedJson) {
    throw new Error(
      `${message}\n  expected: ${expectedJson}\n  actual:   ${actualJson}`,
    );
  }
}

export function assertThrows(fn: () => unknown, message: string): void {
  let threw = false;
  try {
    fn();
  } catch {
    threw = true;
  }
  if (!threw) throw new Error(`Expected throw: ${message}`);
}

export function run(): void {
  let passed = 0;
  const failures: { name: string; error: Error }[] = [];
  for (const c of cases) {
    try {
      c.fn();
      passed += 1;
      console.log(`  \u2713 ${c.name}`);
    } catch (error) {
      failures.push({ name: c.name, error: error as Error });
      console.log(`  \u2717 ${c.name}`);
    }
  }
  console.log("");
  if (failures.length > 0) {
    for (const failure of failures) {
      console.log(`FAIL: ${failure.name}`);
      console.log(`  ${failure.error.message}`);
      console.log("");
    }
  }
  console.log(`${passed}/${cases.length} tests passed`);
  if (failures.length > 0) {
    process.exitCode = 1;
  }
}

const HOUR = 3_600_000;
const MINUTE = 60_000;

/** Parse an ISO UTC string to epoch ms. */
export function utc(iso: string): number {
  return Date.parse(iso);
}

export { HOUR, MINUTE };
