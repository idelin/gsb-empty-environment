import assert = require('assert');

export { assert };

type TestFn = () => void;

const suites: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  suites.push({ name, fn });
}

export function run(): void {
  let failed = 0;
  for (const { name, fn } of suites) {
    try {
      fn();
      process.stdout.write(`ok   ${name}\n`);
    } catch (err) {
      failed++;
      process.stdout.write(`FAIL ${name}\n`);
      process.stderr.write(`${(err as Error).stack ?? err}\n`);
    }
  }
  process.stdout.write(`\n${suites.length - failed}/${suites.length} passed\n`);
  if (failed > 0) process.exitCode = 1;
}
