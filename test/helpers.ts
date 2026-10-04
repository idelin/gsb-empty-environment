// Zero-dependency test harness (node:test is unavailable on Node 16).

export type TestFn = () => Promise<void>;
export type VoidFn = () => void;
export type RejectFn = (e: unknown) => void;

interface TestCase {
  name: string;
  fn: TestFn;
}

const tests: Array<TestCase> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export async function run(): Promise<number> {
  let pass = 0;
  let fail = 0;
  for (const t of tests) {
    try {
      await t.fn();
      pass++;
      console.log('ok   - ' + t.name);
    } catch (e) {
      fail++;
      console.error('FAIL - ' + t.name);
      console.error(e);
    }
  }
  console.log('');
  console.log(pass + ' passed, ' + fail + ' failed, ' + tests.length + ' total');
  if (fail > 0) return 1;
  return 0;
}

export function assert(cond: boolean, msg: string = ''): void {
  if (!cond) throw new Error('assertion failed: ' + msg);
}

export function eq(actual: unknown, expected: unknown, msg: string = ''): void {
  if (actual !== expected) {
    throw new Error('expected ' + String(expected) + ', got ' + String(actual) + '. ' + msg);
  }
}

export function deepEq(actual: unknown, expected: unknown, msg: string = ''): void {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) throw new Error('expected ' + b + ', got ' + a + '. ' + msg);
}

export async function rejects(p: Promise<unknown>, check: RejectFn): Promise<void> {
  try {
    await p;
  } catch (e) {
    check(e);
    return;
  }
  throw new Error('expected promise to reject');
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

// Asserts that p does not settle within ms milliseconds.
// The caller must eventually await or catch p to avoid unhandled rejections.
export async function assertPending(p: Promise<unknown>, ms: number = 25): Promise<void> {
  let settled = false;
  p.then(
    () => {
      settled = true;
    },
    () => {
      settled = true;
    }
  );
  await sleep(ms);
  assert(!settled, 'expected promise to still be pending');
}

export interface Deferred {
  promise: Promise<void>;
  resolve: VoidFn;
  reject: RejectFn;
}

export function deferred(): Deferred {
  let resolveFn: VoidFn = () => undefined;
  let rejectFn: RejectFn = () => undefined;
  const promise: Promise<void> = new Promise((res, rej) => {
    resolveFn = res;
    rejectFn = rej;
  });
  return { promise, resolve: resolveFn, reject: rejectFn };
}
