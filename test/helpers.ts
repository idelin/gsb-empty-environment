export interface TestCase {
  name: string;
  fn: () => void;
}

const tests: TestCase[] = [];

export function test(name: string, fn: () => void): void {
  tests.push({ name, fn });
}

export function getTests(): TestCase[] {
  return tests;
}

export function assertEqual(actual: unknown, expected: unknown, message?: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    throw new Error((message ?? "assertion failed") + "\n  actual:   " + a + "\n  expected: " + e);
  }
}

export function assertTrue(cond: boolean, message?: string): void {
  if (!cond) {
    throw new Error(message ?? "expected condition to be true");
  }
}
