import assert from "node:assert/strict";

const tests = [];

export function test(name, fn) {
  tests.push({ name, fn });
}

export { assert };

export function assertThrows(fn, pattern) {
  assert.throws(fn, pattern instanceof RegExp ? pattern : undefined);
  if (!(pattern instanceof RegExp)) {
    try {
      fn();
    } catch (err) {
      assert.ok(
        String(err && err.message).includes(pattern),
        `expected error containing ${JSON.stringify(pattern)}, got ${err && err.message}`,
      );
      return;
    }
    assert.fail(`expected function to throw containing ${JSON.stringify(pattern)}`);
  }
}

export async function run() {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      await fn();
      console.log(`ok   - ${name}`);
    } catch (err) {
      failed += 1;
      console.error(`FAIL - ${name}`);
      console.error(err);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) {
    process.exit(1);
  }
}
