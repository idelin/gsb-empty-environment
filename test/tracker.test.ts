import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DynamicCliqueTracker } from '../src/tracker.ts';
import { listMaximalCliques } from '../src/bronKerbosch.ts';

const asSets = (cliques: number[][]): Set<string> =>
  new Set(cliques.map((c) => [...c].sort((a, b) => a - b).join(',')));

test('验收场景：社交网络紧密圈子 —— 一条边促成 5 节点完全子图', () => {
  // 两个圈子：{0,1,2,3} 是 K4 缺边 (0,3)？不 —— 直接构造：
  // 顶点 0..4，除边 (0,4) 外两两相连 => 两个极大团 {0,1,2,3} 与 {1,2,3,4}
  const edges: [number, number][] = [];
  for (let i = 0; i < 5; i++) {
    for (let j = i + 1; j < 5; j++) {
      if (!(i === 0 && j === 4)) edges.push([i, j]);
    }
  }
  const tracker = new DynamicCliqueTracker(edges);
  assert.deepEqual(
    asSets(tracker.maximalCliques()),
    new Set(['0,1,2,3', '1,2,3,4']),
  );

  // 查询包含指定节点集合的极大团
  assert.deepEqual(tracker.maximalCliquesContaining([0]), [[0, 1, 2, 3]]);
  assert.deepEqual(
    asSets(tracker.maximalCliquesContaining([1, 2])),
    new Set(['0,1,2,3', '1,2,3,4']),
  );

  // 加入关键边 (0,4)：5 节点完全子图 K5 诞生，两个子团被消解
  const change = tracker.addEdge(0, 4);
  assert.deepEqual(change.born, [[0, 1, 2, 3, 4]]);
  assert.deepEqual(
    asSets(change.dissolved),
    new Set(['0,1,2,3', '1,2,3,4']),
  );
  assert.deepEqual(tracker.maximalCliques(), [[0, 1, 2, 3, 4]]);
  assert.deepEqual(tracker.maximalCliquesContaining([0, 4]), [[0, 1, 2, 3, 4]]);

  // 删除边 (0,4)：正确退化回两个极大团
  const back = tracker.removeEdge(0, 4);
  assert.deepEqual(
    asSets(back.dissolved),
    new Set(['0,1,2,3,4']),
  );
  assert.deepEqual(
    asSets(back.born),
    new Set(['0,1,2,3', '1,2,3,4']),
  );
  assert.deepEqual(
    asSets(tracker.maximalCliques()),
    new Set(['0,1,2,3', '1,2,3,4']),
  );
});

test('删边后退化候选若被现存团包含则不诞生', () => {
  // K4 {0,1,2,3} 加上顶点 4 只与 0,1,2 相连 => 极大团 {0,1,2,3},{0,1,2,4}
  const tracker = new DynamicCliqueTracker([
    [0, 1], [0, 2], [0, 3], [1, 2], [1, 3], [2, 3],
    [0, 4], [1, 4], [2, 4],
  ]);
  // 删 (0,3)：{0,1,2,3} 失效，候选 {1,2,3}（被 {1,2,3}? 不，现存有 {0,1,2,4} 与候选 {0,1,2}）
  // {1,2,3} 不被任何现存团包含 => 诞生；{0,1,2} 被 {0,1,2,4} 包含 => 不诞生
  const change = tracker.removeEdge(0, 3);
  assert.deepEqual(asSets(change.born), new Set(['1,2,3']));
  assert.deepEqual(
    asSets(tracker.maximalCliques()),
    new Set(['0,1,2,4', '1,2,3']),
  );
});

test('重复加边/删不存在的边是空操作', () => {
  const tracker = new DynamicCliqueTracker([[1, 2]]);
  assert.deepEqual(tracker.addEdge(1, 2), { born: [], dissolved: [] });
  assert.deepEqual(tracker.removeEdge(1, 3), { born: [], dissolved: [] });
  assert.deepEqual(tracker.maximalCliques(), [[1, 2]]);
});

test('差分测试：动态增删边后与全量 Bron–Kerbosch 重算一致', () => {
  let seed = 20261004;
  const rand = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31) / 2 ** 31;
  const n = 9;
  const tracker = new DynamicCliqueTracker();
  const fresh = () => asSets(listMaximalCliques(tracker.graph));

  for (let step = 0; step < 300; step++) {
    const u = Math.floor(rand() * n);
    const v = Math.floor(rand() * n);
    if (u === v) continue;
    if (tracker.graph.hasEdge(u, v)) tracker.removeEdge(u, v);
    else tracker.addEdge(u, v);
    assert.deepEqual(
      asSets(tracker.maximalCliques()),
      fresh(),
      `第 ${step} 步 (${u},${v}) 后不一致`,
    );
  }
});
