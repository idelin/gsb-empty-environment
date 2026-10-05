type TestFn = () => void | Promise<void>;

interface RegisteredTest {
  readonly name: string;
  readonly fn: TestFn;
}

const tests: RegisteredTest[] = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function tick(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

export function assert(condition: boolean, message: string): void {
  if (!condition) {
    throw new Error(`assertion failed: ${message}`);
  }
}

export function assertEqual<T>(actual: T, expected: T, message: string): void {
  if (!Object.is(actual, expected)) {
    throw new Error(
      `assertion failed: ${message}\n  expected: ${String(expected)}\n  actual:   ${String(actual)}`,
    );
  }
}

export function assertDeepEqual(actual: unknown, expected: unknown, message: string): void {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) {
    throw new Error(`assertion failed: ${message}\n  expected: ${b}\n  actual:   ${a}`);
  }
}

export async function expectReject(promise: Promise<unknown>, message: string): Promise<Error> {
  try {
    await promise;
  } catch (error) {
    return error as Error;
  }
  throw new Error(`assertion failed: ${message} (expected rejection, got fulfillment)`);
}

export async function runAll(): Promise<void> {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      await Promise.race([
        Promise.resolve().then(fn),
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error("timed out after 2000ms")), 2000),
        ),
      ]);
      console.log(`ok   - ${name}`);
    } catch (error) {
      failed += 1;
      console.error(`FAIL - ${name}`);
      console.error(`       ${(error as Error).message}`);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) {
    process.exitCode = 1;
  }
}
