/**
 * Offline test runner: erases types from the TypeScript sources into
 * `.build/` and executes the convergence suite with plain Node.
 * (No `tsc` available in this environment; see tools/strip-types.mjs.)
 */

import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { stripTypes } from './strip-types.mjs';

const root = process.cwd();
const files = ['src/list-model.ts', 'test/reference.ts', 'test/convergence.test.ts'];

rmSync(join(root, '.build'), { recursive: true, force: true });

for (const file of files) {
  const source = readFileSync(join(root, file), 'utf8');
  const dest = join(root, '.build', file.replace(/\.ts$/, '.js'));
  mkdirSync(dirname(dest), { recursive: true });
  writeFileSync(dest, stripTypes(source));
}

await import(pathToFileURL(join(root, '.build/test/convergence.test.js')).href);
