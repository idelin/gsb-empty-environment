type TestFn = () => void;

const tests: { name: string; fn: TestFn }[] = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export function run(): void {
  let failed = 0;
  for (const t of tests) {
    try {
      t.fn();
      console.log(`ok   - ${t.name}`);
    } catch (e) {
      failed++;
      console.error(`FAIL - ${t.name}`);
      console.error(`       ${(e as Error).stack ?? e}`);
    }
  }
  if (failed > 0) {
    console.error(`\n${failed}/${tests.length} test(s) failed`);
    process.exit(1);
  }
  console.log(`\n${tests.length} tests passed`);
}
