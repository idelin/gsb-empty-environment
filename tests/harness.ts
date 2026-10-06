type TestFn = () => void;

const registry: Array<{ name: string; fn: TestFn }> = [];

export function test(name: string, fn: TestFn): void {
  registry.push({ name, fn });
}

export function runAll(): void {
  let failed = 0;
  for (const { name, fn } of registry) {
    try {
      fn();
      console.log(`ok - ${name}`);
    } catch (err) {
      failed++;
      console.error(`FAIL - ${name}`);
      console.error(err);
    }
  }
  console.log(`${registry.length - failed}/${registry.length} tests passed`);
  if (failed > 0) process.exit(1);
}
