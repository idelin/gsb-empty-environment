import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  exhaustiveKeepSet, greedyKeepSet, costOf, scoreOf, layoutAtoms,
} from '../src/index.ts';
import type { Atom } from '../src/index.ts';

let rng = 20260926;
const rand = () => (rng = (rng * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;

function randomInput(n: number) {
  const widths = Array.from({ length: n }, () => 1 + Math.floor(rand() * 4));
  const keyAtoms = widths.map(() => rand() < 0.25);
  const lineWidth = 4 + Math.floor(rand() * 20);
  const ellipsisWidth = 1 + Math.floor(rand() * 3);
  return { widths, keyAtoms, lineWidth, ellipsisWidth };
}

test('穷举解:可行且保留全部放得下的关键原子', () => {
  for (let t = 0; t < 500; t++) {
    const input = randomInput(3 + Math.floor(rand() * 8));
    const r = exhaustiveKeepSet(input);
    const required = costOf(input.keyAtoms, input.widths, input.ellipsisWidth);
    if (required <= input.lineWidth) {
      assert.ok(r.feasible);
      assert.ok(r.cost <= input.lineWidth);
      for (let i = 0; i < input.widths.length; i++) {
        if (input.keyAtoms[i]) assert.ok(r.kept[i], `key atom ${i} must be kept`);
      }
    }
  }
});

test('贪心对照穷举:随机小布局上最优率 >= 99%', () => {
  let mismatches = 0;
  const total = 2000;
  for (let t = 0; t < total; t++) {
    const input = randomInput(3 + Math.floor(rand() * 10));
    const g = greedyKeepSet(input);
    const e = exhaustiveKeepSet(input);
    if (g.feasible) {
      if (g.score !== e.score) mismatches++;
      // 可行时贪心永不超过穷举最优(关键片段超容量时贪心原样返回,不参与比较)
      assert.ok(g.score <= e.score);
    }
  }
  assert.ok(mismatches / total <= 0.01, `mismatch rate ${mismatches}/${total}`);
});

function makeAtoms(n: number): Atom[] {
  return Array.from({ length: n }, (_, i) => ({
    kind: 'cluster', text: String.fromCharCode(97 + (i % 26)),
    start: i, end: i + 1, dir: 'ltr',
    linkId: null, linkRange: null, entityId: null, breakAfter: false,
  }));
}

test('引擎小布局输出与穷举最优保留目标一致', () => {
  for (let t = 0; t < 200; t++) {
    const n = 5 + Math.floor(rand() * 10);
    const input = randomInput(n);
    if (costOf(input.keyAtoms, input.widths, input.ellipsisWidth) > input.lineWidth) continue;
    const atoms = makeAtoms(n);
    const keyFragments = input.keyAtoms
      .map((k, i) => (k ? { id: `k${i}`, start: i, end: i + 1 } : null))
      .filter((k): k is { id: string; start: number; end: number } => k !== null);
    // 让全部原子落入"剩余区":maxLines=1 强制单行截断
    const r = layoutAtoms(atoms, input.widths, {
      lineWidth: input.lineWidth, maxLines: 1, lineHeight: 1,
      mode: 'middle', keyFragments,
    }, input.ellipsisWidth, 1, 1);
    const kept = new Array<boolean>(n).fill(false);
    for (const item of r.lines[0].items) {
      if (item.kind === 'atom') kept[item.atom.start] = true;
    }
    const engineScore = scoreOf(kept, input.keyAtoms);
    const best = exhaustiveKeepSet(input);
    assert.equal(engineScore, best.score, `case ${t}: ${JSON.stringify(input)}`);
  }
});

test('保留集代价模型:空隙计省略号,全省略计一个', () => {
  assert.equal(costOf([false, false], [1, 1], 2), 2);
  assert.equal(costOf([true, true], [1, 1], 2), 2);
  assert.equal(costOf([true, false], [1, 1], 2), 3);  // 尾部省略
  assert.equal(costOf([false, true], [1, 1], 2), 3);  // 头部省略
  assert.equal(costOf([true, false, true], [1, 1, 1], 2), 4); // 中间空隙
});
