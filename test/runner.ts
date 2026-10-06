export type TestFn = () => void | Promise<void>;

interface TestCase {
  readonly name: string;
  readonly fn: TestFn;
}

declare const console: {
  log(message: string): void;
};

const cases: TestCase[] = [];

export function test(name: string, fn: TestFn): void {
  cases.push({ name, fn });
}

export async function run(): Promise<number> {
  let failures = 0;
  for (const testCase of cases) {
    try {
      await testCase.fn();
      console.log(`ok - ${testCase.name}`);
    } catch (error) {
      failures += 1;
      console.log(`FAIL - ${testCase.name}`);
      const detail = error instanceof Error && error.stack !== undefined ? error.stack : String(error);
      console.log(detail);
    }
  }
  console.log(`${cases.length - failures}/${cases.length} tests passed`);
  return failures;
}

export function assert(condition: unknown, message?: string): asserts condition {
  if (!condition) {
    throw new Error(message ?? 'assertion failed');
  }
}

export function assertEqual<T>(actual: T, expected: T, message?: string): void {
  if (actual !== expected) {
    throw new Error(
      `${message ?? 'values are not equal'}: expected ${format(expected)}, got ${format(actual)}`,
    );
  }
}

export function assertDeepEqual(actual: unknown, expected: unknown, message?: string): void {
  const actualJson = JSON.stringify(actual);
  const expectedJson = JSON.stringify(expected);
  if (actualJson !== expectedJson) {
    throw new Error(`${message ?? 'values differ'}: expected ${expectedJson}, got ${actualJson}`);
  }
}

export async function assertRejects(fn: () => Promise<unknown>, message?: string): Promise<unknown> {
  try {
    await fn();
  } catch (error) {
    return error;
  }
  throw new Error(message ?? 'expected promise to reject');
}

function format(value: unknown): string {
  if (typeof value === 'string') {
    return JSON.stringify(value);
  }
  return String(value);
}
