'use strict';
/**
 * Test compatibility layer.
 *
 * On Node.js >= 18 the built-in `node:test` runner is used directly
 * (`node --test test/` also works). On older Node versions a minimal
 * zero-dependency shim executes the tests synchronously and reports a
 * TAP-like summary on process exit. Only Node.js built-in modules are used.
 */
let nodeTest = null;
try {
  // eslint-disable-next-line global-require
  nodeTest = require('node:test');
} catch (_) {
  nodeTest = null;
}

if (nodeTest) {
  module.exports = { test: nodeTest, describe: nodeTest.describe, it: nodeTest.it };
} else {
  const state = { passed: 0, failed: 0, failures: [] };
  let suiteStack = [];

  function fullName(name) {
    return suiteStack.concat([name]).join(' > ');
  }

  function run(name, fn) {
    const label = fullName(name);
    try {
      const r = fn();
      if (r && typeof r.then === 'function') {
        throw new Error('async tests are not supported by the legacy shim');
      }
      state.passed += 1;
      console.log('ok ' + (state.passed + state.failed) + ' - ' + label);
    } catch (err) {
      state.failed += 1;
      state.failures.push({ label, err });
      console.log('not ok ' + (state.passed + state.failed) + ' - ' + label);
    }
  }

  function describe(name, fn) {
    suiteStack.push(name);
    try {
      fn();
    } finally {
      suiteStack.pop();
    }
  }

  process.on('exit', () => {
    console.log('');
    console.log('# tests ' + (state.passed + state.failed));
    console.log('# pass ' + state.passed);
    console.log('# fail ' + state.failed);
    for (const f of state.failures) {
      console.error('\nFAIL: ' + f.label);
      console.error(f.err && f.err.stack ? f.err.stack : f.err);
    }
    if (state.failed > 0) process.exitCode = 1;
  });

  module.exports = { test: run, describe, it: run };
}
