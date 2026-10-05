/** Tiny zero-dependency test harness. */

type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

function show(value: unknown): string {
  const seen = JSON.stringify(value, (_key, val) => (val === undefined ? '__undefined__' : val));
  return seen === undefined ? 'undefined' : seen;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (a === null || b === null || typeof a !== 'object') return false;
  const aArr = Array.isArray(a);
  const bArr = Array.isArray(b);
  if (aArr !== bArr) return false;
  if (aArr && bArr) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!deepEqual(a[i], b[i])) return false;
    }
    return true;
  }
  const aObj = a as Record<string, unknown>;
  const bObj = b as Record<string, unknown>;
  const ka = Object.keys(aObj);
  const kb = Object.keys(bObj);
  if (ka.length !== kb.length) return false;
  for (const k of ka) {
    if (!Object.prototype.hasOwnProperty.call(bObj, k)) return false;
    if (!deepEqual(aObj[k], bObj[k])) return false;
  }
  return true;
}

export function eq(actual: unknown, expected: unknown, context?: string): void {
  if (!deepEqual(actual, expected)) {
    throw new Error(
      `assertion failed${context ? ` [${context}]` : ''}\n  actual:   ${show(actual)}\n  expected: ${show(expected)}`,
    );
  }
}

export function ok(cond: boolean, context?: string): void {
  if (!cond) throw new Error(`assertion failed${context ? ` [${context}]` : ''}`);
}

export function run(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log(`ok   - ${name}`);
    } catch (err) {
      failed++;
      const msg = err instanceof Error && err.stack ? err.stack : String(err);
      console.error(`FAIL - ${name}\n${msg}`);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exitCode = 1;
}
