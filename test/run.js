// Minimal test runner: imports every *.test.js in this directory.
import { readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));

const tests = [];
globalThis.test = (name, fn) => tests.push({ name, fn });

const files = readdirSync(here).filter((f) => f.endsWith('.test.js')).sort();
for (const f of files) {
  await import(join(here, f));
}

let passed = 0;
let failed = 0;
for (const { name, fn } of tests) {
  try {
    await fn();
    passed++;
    console.log('ok   - ' + name);
  } catch (err) {
    failed++;
    console.error('FAIL - ' + name);
    console.error('       ' + String(err && err.message ? err.message : err).split('\n').join('\n       '));
  }
}
console.log(`\n${passed} passed, ${failed} failed, ${tests.length} total`);
process.exit(failed === 0 ? 0 : 1);
