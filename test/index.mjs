import './replace.test.mjs';
import './mapping.test.mjs';
import './transaction.test.mjs';
import { getTests } from './helpers.mjs';

let pass = 0;
let fail = 0;
for (const { name, fn } of getTests()) {
  try {
    fn();
    pass++;
    console.log('ok   -', name);
  } catch (err) {
    fail++;
    console.error('FAIL -', name);
    console.error(err);
  }
}
console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail === 0 ? 0 : 1);
