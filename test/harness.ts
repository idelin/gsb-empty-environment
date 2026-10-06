let passed = 0;
let failed = 0;
const failures: string[] = [];

export function check(condition: boolean, label: string): void {
  if (condition) {
    passed++;
  } else {
    failed++;
    failures.push(label);
  }
}

export function checkEqual<T>(actual: T, expected: T, label: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  check(a === e, `${label}\n    expected: ${e}\n    actual:   ${a}`);
}

export function checkThrows(fn: () => unknown, label: string): void {
  try {
    fn();
  } catch {
    passed++;
    return;
  }
  failed++;
  failures.push(`${label}\n    expected a throw, got none`);
}

export function section(name: string): void {
  process.stdout.write(`-- ${name}\n`);
}

export function report(): void {
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  for (const f of failures) process.stdout.write(`FAIL: ${f}\n`);
  if (failed > 0) process.exit(1);
}
