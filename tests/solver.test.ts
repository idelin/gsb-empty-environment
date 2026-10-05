import { test } from 'node:test';
import assert from 'node:assert/strict';

import type { ModelSpec } from '../src/model.ts';
import { compileModel } from '../src/model.ts';
import {
  enumerateValidConfigs,
  computePairs,
  solveMinCover,
  isValidConfig,
} from '../src/cover.ts';
import type { PairReq } from '../src/cover.ts';
import { minRowsUnconstrained } from '../src/clique.ts';
import { generate } from '../src/generate.ts';

/** 暴力枚举全部配置子集，求最小覆盖（用于对拍）。 */
function bruteForceMin(n: number, valid: number[], reqs: PairReq[]): number {
  const reqIdx = new Map<string, number>();
  reqs.forEach((r, i) => reqIdx.set(`${r.i},${r.j},${r.vi},${r.vj}`, i));
  const pairs: [number, number][] = [];
  for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) pairs.push([i, j]);
  const covs = valid.map(c => {
    let cov = 0n;
    for (const [i, j] of pairs) {
      const id = reqIdx.get(`${i},${j},${(c >> i) & 1},${(c >> j) & 1}`);
      if (id !== undefined) cov |= 1n << BigInt(id);
    }
    return cov;
  });
  const full = (1n << BigInt(reqs.length)) - 1n;
  for (let k = 1; k <= covs.length; k++) {
    let found = false;
    const rec = (start: number, left: number, acc: bigint) => {
      if (found) return;
      if (left === 0) { if (acc === full) found = true; return; }
      for (let s = start; s <= covs.length - left; s++) {
        rec(s + 1, left - 1, acc | covs[s]);
        if (found) return;
      }
    };
    rec(0, k, 0n);
    if (found) return k;
  }
  throw new Error('暴力求解失败');
}

function solveSpec(spec: ModelSpec) {
  const model = compileModel(spec);
  const valid = enumerateValidConfigs(model.n, model.clauses);
  const { reqs } = computePairs(model.n, valid);
  const cover = solveMinCover(model, valid, reqs);
  return { model, valid, reqs, cover };
}

test('无约束 2 个特性 → 4 个配置', () => {
  const { cover } = solveSpec({ features: ['A', 'B'] });
  assert.equal(cover.configs.length, 4);
  assert.equal(cover.lowerBound, 4);
  assert.equal(cover.usedExactSearch, false);
});

test('无约束 3 个特性 → 4 个配置', () => {
  const { cover } = solveSpec({ features: ['A', 'B', 'C'] });
  assert.equal(cover.configs.length, 4);
});

test('无约束 12 个特性 → 组合下界与构造一致，无需穷举', () => {
  const features = Array.from({ length: 12 }, (_, i) => `F${i}`);
  const { cover, valid } = solveSpec({ features });
  const { rows } = minRowsUnconstrained(12);
  assert.equal(cover.configs.length, rows);
  assert.equal(cover.lowerBound, rows);
  assert.equal(cover.usedExactSearch, false);
  // 输出配置必须合法且覆盖全部 C(12,2)*4 个组合
  assert.equal(valid.length, 4096);
  for (const c of cover.configs) assert.ok(c >= 0 && c < 4096);
});

test('互斥约束：非法组合不可达且解释提及互斥', () => {
  const spec: ModelSpec = { features: ['A', 'B', 'C'], mutex: [['A', 'B']] };
  const result = generate(spec);
  const hit = result.unreachable.find(u => u.req.i === 0 && u.req.j === 1 && u.req.vi === 1 && u.req.vj === 1);
  assert.ok(hit, 'A=1,B=1 应当不可达');
  assert.ok(hit.explanation.join('').includes('互斥'));
  // 与暴力解一致
  const { valid, reqs } = solveSpec(spec);
  assert.equal(result.configs.length, bruteForceMin(3, valid, reqs));
  // 非法配置不得出现在结果中
  for (const c of result.configs) assert.ok(isValidConfig(c, compileModel(spec).clauses));
});

test('蕴含链：A⇒B⇒C 时 A=1,C=0 不可达，解释含完整推导链', () => {
  const result = generate({ features: ['A', 'B', 'C'], implies: [['A', 'B'], ['B', 'C']] });
  const hit = result.unreachable.find(u => u.req.i === 0 && u.req.j === 2 && u.req.vi === 1 && u.req.vj === 0);
  assert.ok(hit, 'A=1,C=0 应当不可达');
  const text = hit.explanation.join('\n');
  assert.ok(text.includes('A=1'));
  assert.ok(text.includes('B=0')); // 逆否链：C=0 ⇒ B=0 ⇒ A=0
  assert.ok(text.includes('矛盾'));
});

test('被强制为常量的特性：A⇒B 且 A、B 互斥 ⇒ A 恒为 0', () => {
  const result = generate({ features: ['A', 'B'], implies: [['A', 'B']], mutex: [['A', 'B']] });
  // A=1 的所有组合不可达
  const a1 = result.unreachable.filter(u => u.req.i === 0 && u.req.vi === 1);
  assert.equal(a1.length, 2); // (A=1,B=0) 与 (A=1,B=1)
  for (const c of result.configs) assert.equal(c & 1, 0);
});

test('至多其一组：组内任两个 1 不可达', () => {
  const result = generate({
    features: ['D', 'E', 'F', 'G'],
    atMostOne: [['D', 'E', 'F']],
  });
  const de = result.unreachable.find(u => u.req.i === 0 && u.req.j === 1 && u.req.vi === 1 && u.req.vj === 1);
  assert.ok(de);
  assert.ok(de.explanation.join('').includes('至多其一'));
});

test('随机小实例与暴力枚举对拍（含最小性证明完整性）', () => {
  let seed = 12345;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  for (let t = 0; t < 60; t++) {
    const n = 2 + Math.floor(rand() * 4); // 2..5 个特性
    const features = Array.from({ length: n }, (_, i) => `F${i}`);
    const spec: ModelSpec = { features, mutex: [], implies: [] };
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const r = rand();
        if (r < 0.15) spec.mutex!.push([features[i], features[j]]);
        else if (r < 0.35) spec.implies!.push(rand() < 0.5 ? [features[i], features[j]] : [features[j], features[i]]);
      }
    }
    const { model, valid, reqs, cover } = solveSpec(spec);
    assert.ok(!cover.aborted, `实例 ${t} 搜索被中止`);
    const brute = bruteForceMin(n, valid, reqs);
    assert.equal(cover.configs.length, brute, `实例 ${t} 配置数不等于暴力最优: ${JSON.stringify(spec)}`);
    assert.ok(cover.lowerBound <= cover.configs.length);
    // 全部输出配置合法
    for (const c of cover.configs) assert.ok(isValidConfig(c, model.clauses));
  }
});

test('非法输入：超过 12 个特性报错', () => {
  const features = Array.from({ length: 13 }, (_, i) => `F${i}`);
  assert.throws(() => generate({ features }), /最多为 12/);
});

test('非法输入：特性名重复 / 未知特性引用', () => {
  assert.throws(() => generate({ features: ['A', 'A'] }), /重复/);
  assert.throws(() => generate({ features: ['A', 'B'], mutex: [['A', 'C']] }), /未知特性/);
});

test('12 特性带约束的实际算例可快速求解', () => {
  const features = Array.from({ length: 12 }, (_, i) => `F${i}`);
  const spec: ModelSpec = {
    features,
    mutex: [['F0', 'F1'], ['F2', 'F3'], ['F4', 'F5']],
    implies: [['F6', 'F0'], ['F7', 'F6'], ['F8', 'F2'], ['F9', 'F8'], ['F10', 'F4']],
    atMostOne: [['F1', 'F3', 'F11']],
  };
  const result = generate(spec);
  assert.ok(result.configs.length >= result.cover.lowerBound);
  assert.equal(result.reachableCount + result.unreachable.length, result.totalPairCombos);
});
