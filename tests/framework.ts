export interface TestCase {
  name: string;
  fn: () => void;
}

export const tests: TestCase[] = [];

export function test(name: string, fn: () => void): void {
  tests.push({ name, fn });
}

export function assert(cond: boolean, message: string): void {
  if (!cond) throw new Error(`assertion failed: ${message}`);
}

export function assertEqual<T>(actual: T, expected: T, message: string): void {
  if (actual !== expected) {
    throw new Error(`${message}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

export function assertDeepEqual(actual: unknown, expected: unknown, message: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${message}: expected ${e}, got ${a}`);
}

export function run(): void {
  let passed = 0;
  const failures: { name: string; error: Error }[] = [];
  for (const { name, fn } of tests) {
    try {
      fn();
      passed++;
      console.log(`ok   ${name}`);
    } catch (err) {
      failures.push({ name, error: err as Error });
      console.log(`FAIL ${name}`);
    }
  }
  console.log(`\n${passed}/${tests.length} tests passed`);
  for (const { name, error } of failures) {
    console.log(`\n--- ${name} ---`);
    console.log(error.stack ?? error.message);
  }
  if (failures.length > 0) process.exit(1);
}
