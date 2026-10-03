'use strict';
/**
 * Test harness: uses the built-in node:test runner when available
 * (Node.js >= 18, run with `node --test`), otherwise falls back to a
 * minimal runner backed by the built-in node:assert module so the suite
 * also runs on older Node versions via `node tests/decorrelate.test.js`.
 */

let test;
try {
  // eslint-disable-next-line global-require
  ({ test } = require('node:test'));
} catch {
  const registered = [];
  test = (name, fn) => registered.push([name, fn]);
  process.on('beforeExit', () => {
    let failed = 0;
    for (const [name, fn] of registered) {
      try {
        fn();
        console.log(`ok - ${name}`);
      } catch (err) {
        failed++;
        console.error(`not ok - ${name}`);
        console.error(err);
      }
    }
    console.log(`\n${registered.length - failed}/${registered.length} tests passed`);
    if (failed > 0) process.exitCode = 1;
  });
}

module.exports = { test };
