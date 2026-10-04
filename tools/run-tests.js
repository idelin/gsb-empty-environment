'use strict';
require('./ts-hook');

const fs = require('fs');
const path = require('path');

const helpers = require('../test/helpers.ts');
const testDir = path.join(__dirname, '..', 'test');
const files = fs
  .readdirSync(testDir)
  .filter((f) => f.endsWith('.test.ts'))
  .sort();

for (const f of files) {
  require(path.join(testDir, f));
}

// Watchdog: if a test deadlocks (e.g. a read that never unblocks), fail
// loudly instead of exiting silently when the event loop drains.
const watchdog = setTimeout(() => {
  console.error('test run timed out (possible deadlock)');
  process.exit(2);
}, 15000);

helpers.run().then(
  (code) => {
    clearTimeout(watchdog);
    process.exit(code);
  },
  (err) => {
    clearTimeout(watchdog);
    console.error(err);
    process.exit(1);
  }
);
