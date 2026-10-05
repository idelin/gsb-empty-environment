let count = 0;
let failures = 0;

export function test(name: string, fn: () => void): void {
  count += 1;
  try {
    fn();
    process.stdout.write(`ok ${count} - ${name}\n`);
  } catch (err) {
    failures += 1;
    process.stdout.write(`not ok ${count} - ${name}\n`);
    console.error(err);
  }
}

export function report(): void {
  process.stdout.write(`# ${count} tests, ${failures} failed\n`);
  if (failures > 0) {
    process.exit(1);
  }
}
