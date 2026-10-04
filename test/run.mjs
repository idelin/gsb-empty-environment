import { readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const files = readdirSync(here).filter((f) => f.endsWith('.test.mjs')).sort();
let passed = 0;
let failed = 0;
for (const f of files) {
  const mod = await import(join(here, f));
  for (const [name, fn] of Object.entries(mod)) {
    if (!name.startsWith('test') || typeof fn !== 'function') continue;
    try {
      await fn();
      passed++;
      console.log('ok   ' + f + ' :: ' + name);
    } catch (err) {
      failed++;
      console.error('FAIL ' + f + ' :: ' + name);
      console.error(err && err.stack ? err.stack : err);
    }
  }
}
console.log('---');
console.log(passed + ' passed, ' + failed + ' failed');
process.exit(failed === 0 ? 0 : 1);
