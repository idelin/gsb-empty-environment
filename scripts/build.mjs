// Dependency-free build: strips the TypeScript type syntax used in src/ and
// emits runnable ESM to dist/. The source intentionally uses only erasable
// syntax (interfaces, type aliases, annotations); with network access you can
// instead run a real compiler: `npx tsc -p tsconfig.json`.
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const src = readFileSync(join(root, 'src', 'mapping.ts'), 'utf8');

const TYPE = '[A-Za-z_][A-Za-z0-9_<>\\[\\]| ]*';
const out = [];
let inInterface = false;

for (const line of src.split('\n')) {
  const trimmed = line.trim();
  if (inInterface) {
    if (trimmed.startsWith('}')) inInterface = false;
    continue;
  }
  if (trimmed.startsWith('export interface ')) {
    if (!trimmed.includes('}')) inInterface = true;
    continue;
  }
  if (trimmed.startsWith('export type ')) continue;

  let text = line;
  if (trimmed.includes('function ')) {
    text = text
      .replace(new RegExp(`:\\s*${TYPE}(?=\\s*[,)])`, 'g'), '')
      .replace(new RegExp(`\\)\\s*:\\s*${TYPE}(\\s*\\{)`), ')$1');
  }
  text = text.replace(new RegExp(`\\b(const|let|var)(\\s+\\w+)\\s*:\\s*${TYPE}(\\s*=)`), '$1$2$3');
  out.push(text);
}

mkdirSync(join(root, 'dist'), { recursive: true });
const target = join(root, 'dist', 'mapping.mjs');
writeFileSync(target, out.join('\n'));
await import(target);
console.log('built', target);
