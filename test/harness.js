"use strict";
const tests = [];
function test(name, fn) {
  tests.push({ name, fn });
}
function run() {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      fn();
      console.log(`ok   - ${name}`);
    } catch (err) {
      failed++;
      console.error(`FAIL - ${name}`);
      console.error(err && err.stack ? err.stack : err);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} passed`);
  if (failed) process.exit(1);
}
module.exports = { test, run };
