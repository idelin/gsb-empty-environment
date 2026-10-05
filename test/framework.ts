/** 零依赖迷你测试框架（环境无法安装 jest 等依赖）。 */

declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};
declare const process: { exitCode: number | undefined };

interface TestCase {
  name: string;
  fn: () => void;
}

const registry: TestCase[] = [];

export function test(name: string, fn: () => void): void {
  registry.push({ name, fn });
}

export function runTests(): void {
  let failed = 0;
  for (const t of registry) {
    try {
      t.fn();
      console.log('ok   ' + t.name);
    } catch (err) {
      failed++;
      console.error('FAIL ' + t.name);
      console.error(err instanceof Error ? err.stack : String(err));
    }
  }
  console.log('');
  console.log(String(registry.length - failed) + '/' + String(registry.length) + ' passed');
  if (failed > 0) process.exitCode = 1;
}

export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error('assert failed: ' + msg);
}

export function assertEqual(actual: unknown, expected: unknown, msg?: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    throw new Error(
      'assertEqual failed' + (msg ? ' [' + msg + ']' : '') +
        '\n  actual:   ' + a + '\n  expected: ' + e
    );
  }
}
