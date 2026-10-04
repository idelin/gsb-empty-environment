import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { stripTypes } from './strip-types.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const outRoot = join(root, '.build');
const files = ['src/coalescer.ts', 'test/run-tests.ts'];

rmSync(outRoot, { recursive: true, force: true });
for (const rel of files) {
  const src = readFileSync(join(root, rel), 'utf8');
  const outPath = join(outRoot, rel.replace(/\.ts$/, '.js'));
  mkdirSync(dirname(outPath), { recursive: true });
  writeFileSync(outPath, stripTypes(src));
}

await import(pathToFileURL(join(outRoot, 'test/run-tests.js')).href);
