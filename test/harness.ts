/** Minimal test harness (Node 16 has no node:test); each test file registers and runs its own tests. */
import { strict as assert } from 'assert';

export { assert };

type TestFn = () => void | Promise<void>;

interface Case {
  name: string;
  fn: TestFn;
}

const cases: Case[] = [];

export function test(name: string, fn: TestFn): void {
  cases.push({ name, fn });
}

export async function run(): Promise<void> {
  let failed = 0;
  for (const c of cases) {
    try {
      await c.fn();
      process.stdout.write('ok   - ' + c.name + '\n');
    } catch (e) {
      failed++;
      process.stdout.write('FAIL - ' + c.name + '\n');
      const err = e as Error;
      process.stdout.write('       ' + (err.stack || err.message).split('\n').join('\n       ') + '\n');
    }
  }
  process.stdout.write(cases.length - failed + '/' + cases.length + ' passed\n');
  if (failed > 0) process.exitCode = 1;
}
