import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/jsonpath/compiler.ts';
import { evaluate } from '../src/jsonpath/interpreter.ts';
import { streamQuery } from '../src/jsonpath/stream.ts';
import { IncrementalResults, VersionedDocument } from '../src/jsonpath/incremental.ts';
import { canonical } from '../src/jsonpath/path.ts';
import type { JsonValue } from '../src/jsonpath/ast.ts';

/** 确定性伪随机（可复现）。 */
function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0xffffffff;
  };
}

function randomTree(rand: () => number, depth: number): JsonValue {
  const r = rand();
  if (depth <= 0 || r < 0.3) {
    const pick = rand();
    if (pick < 0.25) return Math.floor(rand() * 20);
    if (pick < 0.5) return `s${Math.floor(rand() * 5)}`;
    if (pick < 0.65) return pick < 0.575 ? true : false;
    if (pick < 0.8) return null;
    return rand() * 100;
  }
  if (r < 0.65) {
    const len = Math.floor(rand() * 5);
    return Array.from({ length: len }, () => randomTree(rand, depth - 1));
  }
  const keys = ['a', 'b', 'c', 'x/y', 'k z', 'n'];
  const n = Math.floor(rand() * 4);
  const obj: Record<string, JsonValue> = {};
  for (let i = 0; i < n; i++) obj[keys[Math.floor(rand() * keys.length)]!] = randomTree(rand, depth - 1);
  return obj;
}

const QUERIES = [
  '$..a',
  '$..*',
  '$.a[*]',
  '$.a[-1]',
  '$.a[1:3]',
  '$.a[::-1]',
  '$..[?(@.n > 50)]',
  `$['x/y']..b`,
  '$.a[?(@.b)].c',
  '$..a..b',
  '$.a[?(@.n < $.k)].n',
];

test('随机树上：参考解释器 ≡ 流式 ≡ 增量', () => {
  for (let seed = 1; seed <= 40; seed++) {
    const rand = rng(seed);
    const doc = randomTree(rand, 4);
    for (const q of QUERIES) {
      const prog = compile(q);
      const expected = evaluate(doc, prog).map((m) => canonical(m.path)).sort();
      const streamed = streamQuery(doc, prog).matches.map((m) => canonical(m.path)).sort();
      assert.deepEqual(streamed, expected, `stream mismatch seed=${seed} q=${q}`);
      const inc = new IncrementalResults(prog, new VersionedDocument(doc));
      inc.sync();
      const incremental = inc.matches().map((m) => canonical(m.path)).sort();
      assert.deepEqual(incremental, expected, `incremental mismatch seed=${seed} q=${q}`);
    }
  }
});

test('随机编辑序列后增量结果仍与参考解释器一致', () => {
  for (let seed = 100; seed < 115; seed++) {
    const rand = rng(seed);
    const doc = new VersionedDocument({ a: randomTree(rand, 3), keep: 1 });
    const prog = compile('$..a');
    const inc = new IncrementalResults(prog, doc);
    inc.sync();
    for (let step = 0; step < 6; step++) {
      const edits = [];
      const target = randomTree(rand, 2);
      edits.push({ op: 'set' as const, path: ['a'], value: target });
      if (rand() < 0.5) edits.push({ op: 'delete' as const, path: ['a'] });
      doc.apply(edits);
      inc.sync();
      const expected = evaluate(doc.get(), prog).map((m) => canonical(m.path)).sort();
      assert.deepEqual(inc.matches().map((m) => canonical(m.path)).sort(), expected, `seed=${seed} step=${step}`);
    }
  }
});
