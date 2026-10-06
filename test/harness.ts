type TestFn = () => void;
const tests: Array<[string, TestFn]> = [];

export function test(name: string, fn: TestFn): void {
  tests.push([name, fn]);
}

export function ok(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

export function eq<T>(actual: T, expected: T, msg: string): void {
  if (actual !== expected) {
    throw new Error(`assertion failed: ${msg}\n  actual:   ${actual}\n  expected: ${expected}`);
  }
}

export function runAll(): void {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed++;
      console.log(`FAIL ${name}`);
      console.log(String(err instanceof Error ? err.stack : err));
    }
  }
  console.log(`${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exitCode = 1;
}

/** Deterministic PRNG (mulberry32). */
export function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randInt(rand: () => number, lo: number, hi: number): number {
  return lo + Math.floor(rand() * (hi - lo + 1));
}
