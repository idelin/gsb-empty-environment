import assert from "assert";

export { assert };

type TestFn = () => void | Promise<void>;

const tests: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

export async function runAll(): Promise<void> {
  let failed = 0;
  for (const t of tests) {
    try {
      await t.fn();
      process.stdout.write(`ok   - ${t.name}\n`);
    } catch (err) {
      failed++;
      process.stdout.write(`FAIL - ${t.name}\n`);
      process.stderr.write(String(err && (err as Error).stack ? (err as Error).stack : err) + "\n");
    }
  }
  process.stdout.write(`\n${tests.length - failed}/${tests.length} tests passed\n`);
  if (failed > 0) {
    process.exitCode = 1;
  }
}
