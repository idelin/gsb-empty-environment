type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function assert(condition: boolean, message: string): void {
  if (!condition) throw new Error(`assertion failed: ${message}`);
}

export function assertEqual<T>(actual: T, expected: T, message: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${message}\n  expected: ${e}\n  actual:   ${a}`);
}

export function assertThrows(fn: () => void, pattern: RegExp, message: string): void {
  try {
    fn();
  } catch (err) {
    if (pattern.test(String(err))) return;
    throw new Error(`${message}\n  threw, but message did not match ${pattern}: ${err}`);
  }
  throw new Error(`${message}\n  expected a throw matching ${pattern}, but nothing was thrown`);
}

export function run(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed++;
      console.log(`FAIL ${name}`);
      console.log(`     ${String(err).split("\n").join("\n     ")}`);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  process.exit(failed === 0 ? 0 : 1);
}
