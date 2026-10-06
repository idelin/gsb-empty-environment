type TestFn = () => void | Promise<void>;

interface TestCase {
  readonly name: string;
  readonly fn: TestFn;
}

const cases: TestCase[] = [];

export function test(name: string, fn: TestFn): void {
  cases.push({ name, fn });
}

export async function run(): Promise<void> {
  let failed = 0;
  for (const testCase of cases) {
    try {
      await testCase.fn();
      process.stdout.write(`ok - ${testCase.name}\n`);
    } catch (error) {
      failed += 1;
      process.stdout.write(`not ok - ${testCase.name}\n`);
      console.error(error);
    }
  }
  process.stdout.write(
    `\n${cases.length - failed}/${cases.length} tests passed\n`,
  );
  if (failed > 0) {
    process.exitCode = 1;
  }
}
