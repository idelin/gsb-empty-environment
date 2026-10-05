import { strict as assert } from 'node:assert';

export type TestFn = () => void;

export interface TestCase {
  name: string;
  fn: TestFn;
}

const tests: TestCase[] = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function runTests(): number {
  let failed = 0;
  for (const t of tests) {
    try {
      t.fn();
      console.log(`ok   ${t.name}`);
    } catch (err) {
      failed += 1;
      console.error(`FAIL ${t.name}`);
      console.error(err);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} 通过`);
  return failed;
}

export { assert };
