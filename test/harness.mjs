let passed = 0;
let failed = 0;

export function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok   - ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL - ${name}\n       ${err.message}`);
  }
}

export function eq(actual, expected, what = 'value') {
  if (!Object.is(actual, expected)) {
    throw new Error(`${what}: expected ${expected}, got ${actual}`);
  }
}

export function near(actual, expected, what = 'value', eps = 1e-9) {
  if (!(Math.abs(actual - expected) <= eps)) {
    throw new Error(`${what}: expected ~${expected}, got ${actual}`);
  }
}

export function summary() {
  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
}
