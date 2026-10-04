type TestFn = () => void | Promise<void>;

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

export function assertEqual<T>(actual: T, expected: T, msg?: string): void {
  if (actual !== expected) {
    throw new Error(
      `expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}` +
        (msg ? ` (${msg})` : ''),
    );
  }
}

export function assertDeepEqual(actual: unknown, expected: unknown, msg?: string): void {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) {
    throw new Error(`deep equal failed:\n  actual:   ${a}\n  expected: ${b}` + (msg ? ` (${msg})` : ''));
  }
}

export function assertThrows(
  fn: () => void,
  ctor: new (...args: never[]) => Error,
  msg?: string,
): void {
  try {
    fn();
  } catch (err) {
    if (err instanceof ctor) return;
    throw new Error(
      `expected ${ctor.name}, got ${String(err)}` + (msg ? ` (${msg})` : ''),
    );
  }
  throw new Error(`expected ${ctor.name}, but nothing was thrown` + (msg ? ` (${msg})` : ''));
}

export async function assertRejects(
  p: Promise<unknown>,
  ctor: new (...args: never[]) => Error,
  msg?: string,
): Promise<void> {
  try {
    await p;
  } catch (err) {
    if (err instanceof ctor) return;
    throw new Error(
      `expected rejection ${ctor.name}, got ${String(err)}` + (msg ? ` (${msg})` : ''),
    );
  }
  throw new Error(
    `expected rejection ${ctor.name}, but the promise resolved` + (msg ? ` (${msg})` : ''),
  );
}

export async function runAll(): Promise<void> {
  let passed = 0;
  const failures: string[] = [];
  for (const c of cases) {
    try {
      await c.fn();
      passed++;
      console.log(`ok   - ${c.name}`);
    } catch (err) {
      failures.push(c.name);
      console.log(`FAIL - ${c.name}`);
      const e = err as Error;
      console.log(e && e.stack ? e.stack : String(err));
    }
  }
  console.log(`\n${passed}/${cases.length} tests passed`);
  if (failures.length > 0) process.exitCode = 1;
}
