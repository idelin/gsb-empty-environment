export interface TestCase {
  name: string;
  fn: () => void;
}

const tests: TestCase[] = [];

export function test(name: string, fn: () => void): void {
  tests.push({ name, fn });
}

export function runAll(): void {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      process.stdout.write(`ok   ${name}\n`);
    } catch (err) {
      failed++;
      process.stdout.write(`FAIL ${name}\n`);
      console.error(err);
    }
  }
  process.stdout.write(`\n${tests.length - failed}/${tests.length} passed\n`);
  if (failed > 0) process.exit(1);
}
