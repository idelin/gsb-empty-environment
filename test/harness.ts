type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function fail(message: string): never {
  throw new Error(message);
}

export function assert(cond: boolean, message: string): void {
  if (!cond) fail(message);
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  if (typeof a === 'object') {
    const ka = Object.keys(a as object);
    const kb = Object.keys(b as object);
    if (ka.length !== kb.length) return false;
    return ka.every(
      (k) =>
        Object.prototype.hasOwnProperty.call(b, k) &&
        deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
    );
  }
  return false;
}

export function assertDeepEqual(actual: unknown, expected: unknown, message: string): void {
  if (!deepEqual(actual, expected)) {
    fail(`${message}\n  actual:   ${JSON.stringify(actual)}\n  expected: ${JSON.stringify(expected)}`);
  }
}

export function run(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log(`ok   - ${name}`);
    } catch (err) {
      failed++;
      console.error(`FAIL - ${name}`);
      console.error(`       ${(err as Error).message}`);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) {
    process.exitCode = 1;
  }
}
