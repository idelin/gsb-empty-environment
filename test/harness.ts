type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function run(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.error(`FAIL ${name}`);
      console.error(err instanceof Error ? err.stack : String(err));
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exitCode = 1;
}

export function assert(cond: boolean, message: string): void {
  if (!cond) throw new Error(`assertion failed: ${message}`);
}

export function assertEqual<T>(actual: T, expected: T, what: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    throw new Error(`${what}\n  expected: ${e}\n  actual:   ${a}`);
  }
}
