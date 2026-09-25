import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildGraph, resolvePath, detectAmbiguities } from '../src/graph.ts';
import { MigrationError } from '../src/errors.ts';

const noop = {
  name: 'noop',
  input: { type: 'object' as const },
  output: { type: 'object' as const },
  apply: (d: Record<string, unknown>) => d,
};

const edge = (id: string, from: string, to: string, priority: number) => ({ id, from, to, priority, steps: [noop] });

test('确定性选路：最低成本唯一路径', () => {
  const graph = buildGraph([
    edge('a-c', 'a', 'c', 9),
    edge('a-b', 'a', 'b', 1),
    edge('b-c', 'b', 'c', 1),
  ]);
  const plan = resolvePath(graph, 'a', 'c');
  assert.deepEqual(plan.versionPath, ['a', 'b', 'c']);
  assert.equal(plan.cost, 2);
});

test('多步边：版本路径与成本正确', () => {
  const graph = buildGraph([
    {
      id: 'a-b',
      from: 'a',
      to: 'b',
      priority: 3,
      steps: [
        { ...noop, name: 's1' },
        { ...noop, name: 's2' },
        { ...noop, name: 's3' },
      ],
    },
  ]);
  const plan = resolvePath(graph, 'a', 'b');
  assert.equal(plan.edges[0]!.steps.length, 3);
  assert.equal(plan.cost, 3);
});

test('两条同优先路径 => AMBIGUOUS_PATH 并列出候选', () => {
  const graph = buildGraph([
    edge('direct', 'a', 'c', 5),
    edge('via-b-1', 'a', 'b', 2),
    edge('via-b-2', 'b', 'c', 3),
  ]);
  try {
    resolvePath(graph, 'a', 'c');
    assert.fail('should reject');
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'AMBIGUOUS_PATH');
    assert.deepEqual(err.details.candidates, [
      ['a', 'c'],
      ['a', 'b', 'c'],
    ]);
    assert.equal(err.details.tiedPaths!.length, 2);
  }
});

test('detectAmbiguities 扫描全图', () => {
  const graph = buildGraph([
    edge('direct', 'a', 'c', 5),
    edge('a-b', 'a', 'b', 5),
    edge('b-c', 'b', 'c', 0),
  ]);
  const errors = detectAmbiguities(graph);
  assert.equal(errors.length, 1);
  assert.deepEqual(errors[0]!.details.versionPath, ['a', 'c']);
});

test('不可达 => NO_PATH；成环不死循环', () => {
  const graph = buildGraph([
    edge('a-b', 'a', 'b', 1),
    edge('b-a', 'b', 'a', 1),
    edge('c-d', 'c', 'd', 1),
  ]);
  try {
    resolvePath(graph, 'a', 'd');
    assert.fail('should reject');
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'NO_PATH');
  }
  // 自环可以解析到自身起点以外的真实路径
  assert.deepEqual(resolvePath(graph, 'a', 'b').versionPath, ['a', 'b']);
});

test('buildGraph 校验非法定义并定位 location', () => {
  const cases: Array<{ edges: unknown[]; location: string }> = [
    { edges: [], location: '/edges' },
    { edges: [{ to: 'b', priority: 1, steps: [noop] }], location: '/edges[0]/from' },
    { edges: [{ from: 'a', to: 'b', priority: 1, steps: [] }], location: '/edges[0]/steps' },
    { edges: [{ from: 'a', to: 'b', priority: 'x', steps: [noop] }], location: '/edges[0]/priority' },
  ];
  for (const c of cases) {
    try {
      buildGraph(c.edges as never);
      assert.fail('should reject');
    } catch (err) {
      assert.ok(err instanceof MigrationError);
      assert.equal(err.details.code, 'INVALID_GRAPH');
      assert.equal(err.details.location, c.location);
    }
  }

  try {
    buildGraph([
      { id: 'same', from: 'a', to: 'b', priority: 1, steps: [noop] },
      { id: 'same', from: 'a', to: 'c', priority: 1, steps: [noop] },
    ]);
    assert.fail('should reject');
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'INVALID_GRAPH');
  }
});
