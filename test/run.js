import { strict as assert } from 'node:assert';
import typesTests from './types.test.js';
import analyzerTests from './analyzer.test.js';
import precisionTests from './precision.test.js';

const suites = [typesTests, analyzerTests, precisionTests];
let failed = 0;
let passed = 0;

for (const suite of suites) {
  for (const [name, fn] of Object.entries(suite.cases)) {
    try {
      fn(assert);
      passed++;
      console.log(`ok   ${suite.name} > ${name}`);
    } catch (err) {
      failed++;
      console.error(`FAIL ${suite.name} > ${name}`);
      console.error(err);
    }
  }
}

console.log(`\n${passed} 通过, ${failed} 失败`);
if (failed > 0) process.exit(1);
