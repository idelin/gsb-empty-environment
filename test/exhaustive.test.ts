// Property-style test: for many random small layouts, the engine's
// retained content must match the optimum of the retention objective,
// computed here by an independent brute-force enumeration of every
// single-ellipsis placement.

import test from 'node:test';
import assert from 'node:assert/strict';
import type { ClusterItem, MetricTable } from '../src/types.ts';
import { fitsInLines, layoutBlock } from '../src/layout.ts';
import { makeEllipsisItem } from '../src/metrics.ts';
import { compareScore } from '../src/optimize.ts';

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Case {
  items: ClusterItem[];
  table: MetricTable;
  width: number;
  maxLines: number;
}

function randomCase(rand: () => number, index: number): Case {
  const n = 4 + Math.floor(rand() * 9);
  const clusters: Record<string, number> = {};
  const items: ClusterItem[] = [];
  let offset = 0;
  for (let i = 0; i < n; i++) {
    const isSpace = rand() < 0.2;
    const text = isSpace ? ' ' : `c${i}`;
    const width = isSpace ? 3 : 2 + Math.floor(rand() * 7);
    if (!isSpace) clusters[text] = width;
    items.push({
      kind: 'cluster',
      text,
      srcStart: offset,
      srcEnd: offset + text.length,
      dir: 'ltr',
      key: !isSpace && rand() < 0.3,
      required: false,
      atomicId: 0,
      breakAfter: isSpace,
      width,
    });
    offset += text.length;
  }
  for (const it of items) {
    if (it.key && rand() < 0.4) {
      it.required = true;
      it.keyId = `req-${index}`;
    }
  }
  // one random unbreakable group
  if (n >= 3 && rand() < 0.4) {
    const start = Math.floor(rand() * (n - 2));
    const len = 2 + Math.floor(rand() * 2);
    for (let i = start; i < Math.min(start + len, n); i++) items[i].atomicId = 1;
  }
  const ellipsisWidth = 4 + Math.floor(rand() * 6);
  const table: MetricTable = {
    version: 1,
    defaultWidth: 6,
    spaceWidth: 3,
    ellipsisWidth,
    clusters,
  };
  const width = ellipsisWidth + 2 + Math.floor(rand() * 34);
  const maxLines = 1 + Math.floor(rand() * 2);
  return { items, table, width, maxLines };
}

function cutLegal(items: ClusterItem[], pos: number): boolean {
  if (pos <= 0 || pos >= items.length) return true;
  const prev = items[pos - 1];
  const next = items[pos];
  return !(prev.atomicId !== 0 && prev.atomicId === next.atomicId);
}

function isSpace(it: ClusterItem): boolean {
  return /^\s+$/.test(it.text);
}

function trimEndItems(arr: ClusterItem[]): ClusterItem[] {
  const out = arr.slice();
  while (out.length && isSpace(out[out.length - 1])) out.pop();
  return out;
}

function trimStartItems(arr: ClusterItem[]): ClusterItem[] {
  const out = arr.slice();
  while (out.length && isSpace(out[0])) out.shift();
  return out;
}

function stats(kept: ClusterItem[]): number[] {
  let req = 0;
  let key = 0;
  let total = 0;
  for (const it of kept) {
    if (it.required) req++;
    if (it.key) key++;
    if (!isSpace(it)) total++;
  }
  return [req, key, total];
}

test('engine retention matches the exhaustive optimum on random small layouts', () => {
  const rand = mulberry32(20260926);
  const ITERATIONS = 400;
  let truncatedCases = 0;

  for (let iter = 0; iter < ITERATIONS; iter++) {
    const { items, table, width, maxLines } = randomCase(rand, iter);
    const source = items.map((it) => it.text).join('');
    const result = layoutBlock(items, 'ltr', source, { width, maxLines, mode: 'key' }, table);
    if (!result.truncated || result.degraded) continue;
    truncatedCases++;

    // independent brute force over every placement (a, b)
    const ellipsis = makeEllipsisItem('ltr');
    ellipsis.width = table.ellipsisWidth!;
    let best: number[] | null = null;
    for (let a = 0; a <= items.length; a++) {
      if (!cutLegal(items, a)) continue;
      for (let b = 0; b <= items.length - a; b++) {
        if (!cutLegal(items, items.length - b)) continue;
        const prefix = trimStartItems(trimEndItems(items.slice(0, a)));
        const suffix = trimEndItems(trimStartItems(b > 0 ? items.slice(items.length - b) : []));
        const feasible = fitsInLines([...prefix, ellipsis, ...suffix], width, maxLines);
        if (!feasible) continue;
        const score = stats([...prefix, ...suffix]);
        if (!best || compareScore(score, best) > 0) best = score;
      }
    }

    // engine's achieved stats from its visible clusters
    const engineKept: ClusterItem[] = [];
    for (const row of result.lines) {
      for (const it of row) {
        if (it.kind !== 'cluster') continue;
        engineKept.push(items.find((orig) => orig.srcStart === it.srcStart)!);
      }
    }
    const engineScore = stats(engineKept);
    assert.ok(result.lines.length <= maxLines, `case ${iter}: within line budget`);
    assert.ok(best !== null, `case ${iter}: at least the empty placement is feasible`);
    assert.deepEqual(
      engineScore,
      best,
      `case ${iter}: engine retention [${engineScore}] != optimal [${best}] ` +
        `(n=${items.length} width=${width} maxLines=${maxLines})`,
    );
  }
  assert.ok(truncatedCases > 100, `enough truncated cases exercised (${truncatedCases})`);
});
