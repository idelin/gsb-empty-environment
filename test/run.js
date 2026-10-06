'use strict';

const tests = [];
global.test = (name, fn) => tests.push({ name, fn });

require('./chunking.test');
require('./checkpoint.test');
require('./incremental.test');

let failed = 0;
for (const { name, fn } of tests) {
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.log(`FAIL ${name}`);
    console.log(String(err && err.stack ? err.stack : err).replace(/^/gm, '     '));
  }
}
console.log(`\n${tests.length - failed}/${tests.length} passed`);
process.exit(failed === 0 ? 0 : 1);
