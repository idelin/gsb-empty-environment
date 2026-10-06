export type TestFn = () => void;

interface Case {
  name: string;
  fn: TestFn;
}

const cases: Case[] = [];

export function test(name: string, fn: TestFn): void {
  cases.push({ name, fn });
}

export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

export function deepEq(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  const aArr = Array.isArray(a);
  if (aArr !== Array.isArray(b)) return false;
  if (aArr) {
    const bb = b as readonly unknown[];
    return a.length === bb.length && a.every((v, i) => deepEq(v, bb[i]));
  }
  const ao = a as Record<string, unknown>;
  const bo = b as Record<string, unknown>;
  const keys = Object.keys(ao);
  return (
    keys.length === Object.keys(bo).length &&
    keys.every((k) => Object.prototype.hasOwnProperty.call(bo, k) && deepEq(ao[k], bo[k]))
  );
}

function fmt(v: unknown): string {
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

export function assertDeepEqual(actual: unknown, expected: unknown, msg: string): void {
  if (!deepEq(actual, expected)) {
    throw new Error(`${msg}\n    actual: ${fmt(actual)}\n  expected: ${fmt(expected)}`);
  }
}

export function run(): void {
  let failed = 0;
  for (const c of cases) {
    try {
      c.fn();
      console.log(`ok   - ${c.name}`);
    } catch (e) {
      failed++;
      console.error(`FAIL - ${c.name}\n  ${e instanceof Error ? e.message : String(e)}`);
    }
  }
  console.log(`${cases.length - failed}/${cases.length} tests passed`);
  if (failed > 0) process.exitCode = 1;
}
