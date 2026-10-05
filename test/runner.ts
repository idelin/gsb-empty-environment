interface TestCase {
  name: string;
  fn: () => unknown;
}

const tests: TestCase[] = [];

export function test(name: string, fn: () => unknown): void {
  tests.push({ name, fn });
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => { setTimeout(resolve, ms); });
}

export async function waitFor(cond: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: condition not met within ' + timeoutMs + 'ms');
    await sleep(5);
  }
}

export async function run(): Promise<void> {
  let failed = 0;
  for (const t of tests) {
    try {
      await t.fn();
      console.log('ok   - ' + t.name);
    } catch (err) {
      failed += 1;
      console.error('FAIL - ' + t.name);
      console.error(err);
    }
  }
  console.log('---');
  console.log(tests.length + ' tests, ' + (tests.length - failed) + ' passed, ' + failed + ' failed');
  if (failed > 0) process.exit(1);
}
