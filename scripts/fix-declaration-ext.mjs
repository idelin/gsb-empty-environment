/**
 * Post-build normalization for emitted declaration files.
 *
 * The sources use explicit ".ts" import extensions (required by the
 * rewriteRelativeImportExtensions compiler option). The emitted ".js" files
 * already reference ".js", but the generated ".d.ts" files keep the ".ts"
 * suffix, which is invalid for downstream consumers using standard TypeScript.
 * This script rewrites relative ".ts" specifiers inside every ".d.ts" file.
 */
import { readdirSync, readFileSync, writeFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const root = new URL('../dist/', import.meta.url).pathname;

function walk(dir) {
  const entries = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      entries.push(...walk(full));
    } else if (entry.endsWith('.d.ts')) {
      entries.push(full);
    }
  }
  return entries;
}

let rewritten = 0;
for (const file of walk(root)) {
  const source = readFileSync(file, 'utf8');
  const normalized = source.replace(/(from\s+['"]\.[^'"]+?)\.ts(['"])/g, '$1.js$2');
  if (normalized !== source) {
    writeFileSync(file, normalized);
    rewritten += 1;
  }
}

console.log(`fix-declaration-ext: normalized ${rewritten} declaration file(s)`);
