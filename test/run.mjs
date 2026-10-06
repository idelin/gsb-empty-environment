import { getTests } from './helpers.mjs';

await import('./normalize.test.mjs');
await import('./ops.test.mjs');

let failed = 0;
for (const { name, fn } of getTests()) {
  try {
    fn();
    console.log('ok   - ' + name);
  } catch (err) {
    failed++;
    console.error('FAIL - ' + name);
    console.error(err && err.message ? err.message : err);
  }
}
if (failed > 0) {
  console.error(failed + ' test(s) failed');
  process.exit(1);
}
console.log(getTests().length + ' tests passed');
