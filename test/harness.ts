/** Minimal test harness (Node 16 has no built-in runner). */

type TestFn = () => void;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function ok(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

export function eq(actual: unknown, expected: unknown, msg: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${msg}\n  actual:   ${a}\n  expected: ${e}`);
}

export function closeTo(actual: number, expected: number, msg: string, eps = 1e-9): void {
  if (Math.abs(actual - expected) > eps * Math.max(1, Math.abs(expected))) {
    throw new Error(`${msg}: actual ${actual}, expected ${expected}`);
  }
}

/** Deterministic PRNG (mulberry32) for property-style tests. */
export function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let z = s;
    z = Math.imul(z ^ (z >>> 15), z | 1);
    z ^= z + Math.imul(z ^ (z >>> 7), z | 61);
    return ((z ^ (z >>> 14)) >>> 0) / 4294967296;
  };
}

export function run(): void {
  let failed = 0;
  for (const t of tests) {
    try {
      t.fn();
      process.stdout.write(`ok   ${t.name}\n`);
    } catch (e) {
      failed += 1;
      process.stdout.write(`FAIL ${t.name}\n  ${(e as Error).message}\n`);
    }
  }
  process.stdout.write(`\n${tests.length - failed}/${tests.length} passed\n`);
  if (failed > 0) process.exit(1);
}
