import { Rational, cmp, formatRational } from '../src/rational.js';

interface TestCase {
  readonly name: string;
  readonly fn: () => void;
}

const cases: TestCase[] = [];

export function test(name: string, fn: () => void): void {
  cases.push({ name, fn });
}

export function runAll(): boolean {
  let failed = 0;
  for (const c of cases) {
    try {
      c.fn();
      console.log(`ok - ${c.name}`);
    } catch (err) {
      failed += 1;
      console.error(`FAIL - ${c.name}`);
      console.error(err);
    }
  }
  console.log(`${cases.length - failed}/${cases.length} tests passed`);
  return failed === 0;
}

export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

export function assertThrows(fn: () => void, ctor: new (...args: any[]) => Error, msg: string): void {
  try {
    fn();
  } catch (err) {
    if (err instanceof ctor) return;
    throw new Error(`${msg}: expected ${ctor.name}, got ${String(err)}`);
  }
  throw new Error(`${msg}: expected ${ctor.name}, but nothing was thrown`);
}

export function assertClose(actual: number, expected: number, tol: number, msg: string): void {
  assert(Math.abs(actual - expected) <= tol, `${msg}: |${actual} - ${expected}| > ${tol}`);
}

export function assertRationalEqual(actual: Rational, expected: Rational, msg: string): void {
  assert(
    cmp(actual, expected) === 0,
    `${msg}: expected ${formatRational(expected)}, got ${formatRational(actual)}`,
  );
}

/** Deterministic pseudo-random doubles in [0, 1). */
export function lcg(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}
