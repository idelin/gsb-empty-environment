type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function assert(cond: boolean, message: string): void {
  if (!cond) throw new Error('assertion failed: ' + message);
}

export function assertEqual<T>(actual: T, expected: T, what: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    throw new Error(`${what}: expected ${e}, got ${a}`);
  }
}

export function runAll(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log('ok   - ' + name);
    } catch (err) {
      failed++;
      console.log('FAIL - ' + name);
      console.log('       ' + String((err as Error).message));
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exitCode = 1;
}

/** Deterministic PRNG (mulberry32) for reproducible randomized tests. */
export function prng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
