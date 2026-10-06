import assert from "node:assert/strict";

const tests: { name: string; fn: () => void }[] = [];

export function test(name: string, fn: () => void): void {
  tests.push({ name, fn });
}

export { assert };

export function run(): void {
  let failed = 0;
  for (const t of tests) {
    try {
      t.fn();
      console.log(`ok - ${t.name}`);
    } catch (err) {
      failed++;
      console.error(`FAIL - ${t.name}`);
      console.error(err);
    }
  }
  if (failed > 0) {
    console.error(`${failed}/${tests.length} tests failed`);
    process.exit(1);
  }
  console.log(`${tests.length} tests passed`);
}

/** Deterministic PRNG for the fuzz tests. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
