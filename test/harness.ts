// Minimal test harness (node:test is unavailable on Node 16).
import assert from 'node:assert';

declare const process: any;

type TestFn = () => void;

let passed = 0;
let failed = 0;
const failures: string[] = [];

export function test (name: string, fn: TestFn) {
  try {
    fn();
    passed++;
    console.log('ok   - ' + name);
  } catch (err) {
    failed++;
    let msg = String(err);
    if (err instanceof Error && err.stack) msg = err.stack;
    failures.push(name + '\n  ' + msg);
    console.log('FAIL - ' + name);
  }
}

export function report () {
  for (const f of failures) console.log('\n' + f);
  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed > 0) process.exitCode = 1;
}

export { assert };
