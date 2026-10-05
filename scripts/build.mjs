// Transpiles src/*.ts to dist/*.js (CommonJS) using Babel's TypeScript preset.
// Type-checking is done separately by `npm run typecheck` (requires tsc).
import { transformFileSync } from '@babel/core';
import presetTs from '@babel/preset-typescript';
import pluginCjs from '@babel/plugin-transform-modules-commonjs';
import { mkdirSync, readdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const srcDir = new URL('../src', import.meta.url).pathname;
const outDir = new URL('../dist', import.meta.url).pathname;
mkdirSync(outDir, { recursive: true });

const files = readdirSync(srcDir).filter((f) => f.endsWith('.ts') && !f.endsWith('.d.ts'));
for (const f of files) {
  const out = transformFileSync(join(srcDir, f), {
    filename: f,
    presets: [presetTs],
    plugins: [pluginCjs],
    babelrc: false,
    configFile: false,
  });
  writeFileSync(join(outDir, f.replace(/\.ts$/, '.js')), out.code + '\n');
}
console.log('built ' + files.length + ' files -> dist/');
