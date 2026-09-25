import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import type { GlyphMetrics, TableNode } from './types.ts';

const repoRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

export function loadTableFixture(name = 'table.json'): TableNode {
  const raw = readFileSync(path.join(repoRoot, 'fixtures', name), 'utf8');
  return JSON.parse(raw) as TableNode;
}

export function loadGlyphFixture(name = 'glyphs.json'): GlyphMetrics {
  const raw = readFileSync(path.join(repoRoot, 'fixtures', name), 'utf8');
  return JSON.parse(raw) as GlyphMetrics;
}
