#!/usr/bin/env node
/** Run every bundled test file with Node and report a summary. */
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const dir = path.join(root, 'dist-test');
const files = fs.readdirSync(dir).filter((f) => f.endsWith('.test.js')).sort();

let failed = 0;
for (const f of files) {
  console.log('--- ' + f + ' ---');
  try {
    const out = execFileSync(process.execPath, [path.join(dir, f)], { encoding: 'utf8' });
    process.stdout.write(out);
  } catch (e) {
    failed++;
    if (e.stdout) process.stdout.write(e.stdout);
    if (e.stderr) process.stdout.write(e.stderr);
  }
}
console.log('---');
if (failed > 0) {
  console.log(failed + ' test file(s) failed');
  process.exit(1);
}
console.log('all test files passed');
