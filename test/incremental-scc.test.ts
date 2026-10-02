import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalSCC,
  type SCCComponent,
} from '../src/incremental-scc.ts';

function comp(id: number, members: number[]): SCCComponent {
  return { id, members };
}

/** 验收场景：三角环合并 -> 挂接 D -> 删边精准分裂 */
test('acceptance: triangle merge, attach D, split on edge removal', () => {
  const g = new IncrementalSCC();
  g.addVertex(1); // A
  g.addVertex(2); // B
  g.addVertex(3); // C

  // A->B, B->C：尚不构成环，无合并
  assert.deepEqual(g.addEdge(1, 2), []);
  assert.deepEqual(g.addEdge(2, 3), []);
  assert.equal(g.componentOf(1), 1);
  assert.equal(g.componentOf(3), 3);

  // C->A：闭合成环，三个单节点分量合并为分量 1（最小 ID）
  const mergeDiff = g.addEdge(3, 1);
  assert.deepEqual(mergeDiff, [
    { kind: 'merge', component: comp(1, [1, 2, 3]), absorbed: [2, 3] },
  ]);
  assert.equal(g.componentOf(2), 1);

  // 加入独立节点 D 及边 C->D：D 无回边，保持独立
  g.addVertex(4); // D
  assert.deepEqual(g.addEdge(3, 4), []);
  assert.equal(g.componentOf(4), 4);

  // 删除 B->C：环被打破，精准分裂为 A、B、C 三个单节点分量
  const splitDiff = g.removeEdge(2, 3);
  assert.deepEqual(splitDiff, [
    {
      kind: 'split',
      from: 1,
      components: [comp(1, [1]), comp(2, [2]), comp(3, [3])],
    },
  ]);

  // D 的归属不受影响
  assert.equal(g.componentOf(4), 4);
  assert.deepEqual(g.members(4), [4]);
  assert.deepEqual(g.components(), [
    comp(1, [1]),
    comp(2, [2]),
    comp(3, [3]),
    comp(4, [4]),
  ]);
});

test('merge absorbs multiple existing components, id is min member', () => {
  const g = new IncrementalSCC();
  for (const v of [10, 20, 30, 40]) g.addVertex(v);
  // 两个独立环：{10,20} 与 {30,40}
  g.addEdge(10, 20);
  g.addEdge(20, 10);
  g.addEdge(30, 40);
  g.addEdge(40, 30);
  assert.deepEqual(g.components(), [comp(10, [10, 20]), comp(30, [30, 40])]);

  // 单向连接不合并
  assert.deepEqual(g.addEdge(20, 30), []);
  // 回边 40->10 形成大环，合并为一个分量，ID 取最小成员 10
  const diffs = g.addEdge(40, 10);
  assert.deepEqual(diffs, [
    { kind: 'merge', component: comp(10, [10, 20, 30, 40]), absorbed: [30] },
  ]);
  assert.equal(g.componentOf(40), 10);
});

test('partial split: only the broken cycle separates', () => {
  const g = new IncrementalSCC();
  for (const v of [1, 2, 3, 4]) g.addVertex(v);
  // 环 1->2->3->1，且 3->4->2（4 也在环上：2->3->4->2）
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  g.addEdge(3, 1);
  g.addEdge(3, 4);
  g.addEdge(4, 2);
  assert.deepEqual(g.components(), [comp(1, [1, 2, 3, 4])]);

  // 删除 3->1 后：{2,3,4} 仍为环，1 单独分裂出去
  const diffs = g.removeEdge(3, 1);
  assert.deepEqual(diffs, [
    { kind: 'split', from: 1, components: [comp(1, [1]), comp(2, [2, 3, 4])] },
  ]);
});

test('removing a non-cycle edge keeps the component intact', () => {
  const g = new IncrementalSCC();
  for (const v of [1, 2, 3]) g.addVertex(v);
  g.addEdge(1, 2);
  g.addEdge(2, 1);
  g.addEdge(1, 3);
  g.addEdge(3, 1);
  g.addEdge(2, 3); // 冗余边
  assert.deepEqual(g.components(), [comp(1, [1, 2, 3])]);
  assert.deepEqual(g.removeEdge(2, 3), []); // 删除后仍是同一分量
  assert.deepEqual(g.components(), [comp(1, [1, 2, 3])]);
});

test('vertex removal splits component and emits vertex-removed first', () => {
  const g = new IncrementalSCC();
  for (const v of [1, 2, 3]) g.addVertex(v);
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  g.addEdge(3, 1);
  assert.deepEqual(g.components(), [comp(1, [1, 2, 3])]);

  const diffs = g.removeVertex(2);
  assert.deepEqual(diffs, [
    { kind: 'vertex-removed', vertex: 2, component: comp(1, [2]) },
    { kind: 'split', from: 1, components: [comp(1, [1]), comp(3, [3])] },
  ]);
  assert.deepEqual(g.components(), [comp(1, [1]), comp(3, [3])]);
});

test('removing a singleton vertex removes its component', () => {
  const g = new IncrementalSCC();
  g.addVertex(7);
  const diffs = g.removeVertex(7);
  assert.deepEqual(diffs, [
    { kind: 'vertex-removed', vertex: 7, component: comp(7, [7]) },
  ]);
  assert.deepEqual(g.components(), []);
  assert.equal(g.vertexCount, 0);
});

test('no-op and error cases', () => {
  const g = new IncrementalSCC();
  g.addVertex(1);
  g.addVertex(2);
  g.addEdge(1, 2);

  assert.deepEqual(g.addEdge(1, 2), []); // 重复边
  assert.deepEqual(g.removeEdge(2, 1), []); // 不存在的边
  assert.deepEqual(g.addEdge(1, 1), []); // 自环不改变分量
  assert.equal(g.componentOf(1), 1);

  assert.throws(() => g.addVertex(1), /already exists/);
  assert.throws(() => g.addEdge(1, 9), /does not exist/);
  assert.throws(() => g.removeVertex(9), /does not exist/);
});

test('self-loop on fresh vertex keeps singleton component', () => {
  const g = new IncrementalSCC();
  g.addVertex(5);
  assert.deepEqual(g.addEdge(5, 5), []);
  assert.deepEqual(g.components(), [comp(5, [5])]);
  assert.deepEqual(g.removeEdge(5, 5), []);
});

test('random mutations match brute-force Tarjan reference', () => {
  // 与暴力全量 Tarjan 对拍：每步变更后分量划分必须一致
  const g = new IncrementalSCC();
  const vertices = [1, 2, 3, 4, 5, 6];
  const edges = new Set<string>();
  for (const v of vertices) g.addVertex(v);

  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;

  for (let step = 0; step < 500; step++) {
    const u = vertices[Math.floor(rand() * vertices.length)];
    const v = vertices[Math.floor(rand() * vertices.length)];
    const key = `${u}->${v}`;
    if (edges.has(key)) {
      edges.delete(key);
      g.removeEdge(u, v);
    } else {
      edges.add(key);
      g.addEdge(u, v);
    }
    assert.deepEqual(g.components(), bruteForceSCC(vertices, edges), `step ${step}`);
  }
});

/** 暴力参考实现：全量 Tarjan */
function bruteForceSCC(vertices: number[], edges: Set<string>): SCCComponent[] {
  const adj = new Map<number, number[]>();
  for (const v of vertices) adj.set(v, []);
  for (const e of edges) {
    const [u, v] = e.split('->').map(Number);
    adj.get(u)!.push(v);
  }

  const index = new Map<number, number>();
  const lowlink = new Map<number, number>();
  const onStack = new Set<number>();
  const stack: number[] = [];
  const result: SCCComponent[] = [];
  let counter = 0;

  const strongConnect = (v: number): void => {
    index.set(v, counter);
    lowlink.set(v, counter);
    counter += 1;
    stack.push(v);
    onStack.add(v);
    for (const w of adj.get(v)!) {
      if (!index.has(w)) {
        strongConnect(w);
        lowlink.set(v, Math.min(lowlink.get(v)!, lowlink.get(w)!));
      } else if (onStack.has(w)) {
        lowlink.set(v, Math.min(lowlink.get(v)!, index.get(w)!));
      }
    }
    if (lowlink.get(v) === index.get(v)) {
      const scc: number[] = [];
      let w: number;
      do {
        w = stack.pop()!;
        onStack.delete(w);
        scc.push(w);
      } while (w !== v);
      scc.sort((a, b) => a - b);
      result.push({ id: scc[0], members: scc });
    }
  };

  for (const v of vertices) {
    if (!index.has(v)) strongConnect(v);
  }
  result.sort((a, b) => a.id - b.id);
  return result;
}
