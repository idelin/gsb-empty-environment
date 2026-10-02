import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  EntityGraph, IntegrityInspector, InvariantViolationError,
  findDanglingReferences, findOrphanCycles, analyzeSkew,
} from '../src/index.ts';
import type { MessageDescriptor, DecodedMessage } from '../src/index.ts';

function buildGraph(): EntityGraph {
  const g = new EntityGraph();
  g.addEntity('User', 1, [{ field: 'referrerId', targetType: 'User', targetId: '2' }]);
  g.addEntity('User', 2, [{ field: 'referrerId', targetType: 'User', targetId: '1' }]);
  g.addRoot('User', 1);
  return g;
}

test('巡检: 悬挂引用被精确定位', () => {
  const g = buildGraph();
  g.addEntity('Order', 100, [
    { field: 'userId', targetType: 'User', targetId: '1' },
    { field: 'userId', targetType: 'User', targetId: '999' }, // 不存在
  ]);
  const dangling = findDanglingReferences(g);
  assert.equal(dangling.length, 1);
  assert.equal(dangling[0].from, 'Order#100');
  assert.equal(dangling[0].targetId, '999');
});

test('巡检: 孤立环路（根不可达的环）被发现，可达环不算孤立', () => {
  const g = buildGraph(); // 1 <-> 2，根为 1 → 可达，非孤立
  assert.equal(findOrphanCycles(g).length, 0);
  // 加入互相引用、且与根断开的 3-4 环
  g.addEntity('User', 3, [{ field: 'referrerId', targetType: 'User', targetId: '4' }]);
  g.addEntity('User', 4, [{ field: 'referrerId', targetType: 'User', targetId: '3' }]);
  const orphans = findOrphanCycles(g);
  assert.equal(orphans.length, 1);
  assert.deepEqual(orphans[0].members, ['User#3', 'User#4']);
});

test('巡检: 自环也是孤立环路；无根时以零入度节点为根', () => {
  const g = new EntityGraph();
  g.addEntity('N', 'a', [{ field: 'next', targetType: 'N', targetId: 'a' }]); // 自环
  g.addEntity('N', 'c', [{ field: 'next', targetType: 'N', targetId: 'd' }]);
  g.addEntity('N', 'd');
  // 无显式根：零入度的 c 成为根，可达 {c, d}；a 的自环不可达 → 孤立
  const orphans = findOrphanCycles(g);
  assert.equal(orphans.length, 1);
  assert.deepEqual(orphans[0].members, ['N#a']);
});

test('巡检: 数据倾斜热点与 Gini 系数', () => {
  const g = new EntityGraph();
  g.addEntity('Hot', 'x');
  for (let i = 0; i < 20; i++) {
    g.addEntity('Cold', `c${i}`, [{ field: 'ref', targetType: 'Hot', targetId: 'x' }]);
  }
  const skew = analyzeSkew(g, 3);
  assert.equal(skew.maxInDegree, 20);
  assert.deepEqual(skew.hotKeys, ['Hot#x']);
  assert.ok(skew.gini > 0.8, `gini=${skew.gini} 应显著大于 0`);
  assert.ok(skew.maxToMeanRatio > 10);
  // 均匀分布：无热点、Gini 为 0
  const flat = new EntityGraph();
  for (let i = 0; i < 4; i++) {
    flat.addEntity('N', `n${i}`, [{ field: 'ref', targetType: 'N', targetId: `n${(i + 1) % 4}` }]);
  }
  const flatSkew = analyzeSkew(flat);
  assert.equal(flatSkew.gini, 0);
  assert.deepEqual(flatSkew.hotKeys, []);
});

test('巡检: 重复主键与自定义不变式违规', () => {
  const g = buildGraph();
  g.addEntity('User', 1); // 重复 id
  g.addEntity('User', 9); // 无任何引用边
  const inspector = new IntegrityInspector(g);
  inspector.addInvariant('no-orphan-users', (graph) => {
    const violations = [];
    for (const e of graph.entities.values()) {
      if (e.type === 'User' && e.refs.length === 0) {
        violations.push({ entity: e.key, detail: 'user without referrer edge' });
      }
    }
    return violations;
  });
  const report = inspector.inspect();
  assert.ok(report.invariantViolations.some((v) => v.invariant === 'unique-entity-id'));
  assert.ok(report.invariantViolations.some((v) => v.invariant === 'no-orphan-users'));
  assert.deepEqual(report.stats.duplicateIds, ['User#1']);
});

test('巡检: assertClean 在违规时抛出并携带报告；干净图通过', () => {
  const dirty = buildGraph();
  dirty.addEntity('Order', 1, [{ field: 'userId', targetType: 'User', targetId: '404' }]);
  const inspector = new IntegrityInspector(dirty);
  assert.throws(() => inspector.assertClean(), InvariantViolationError);
  try {
    inspector.assertClean();
    assert.unreachable();
  } catch (e) {
    const report = (e as InvariantViolationError).report as { danglingRefs: unknown[] };
    assert.equal(report.danglingRefs.length, 1);
  }
  // 干净图：无悬挂、无孤立环、无热点、无违规
  const clean = new EntityGraph();
  clean.addEntity('U', 1, [{ field: 'ref', targetType: 'U', targetId: '2' }]);
  clean.addEntity('U', 2, [{ field: 'ref', targetType: 'U', targetId: '1' }]);
  clean.addRoot('U', 1);
  const report = new IntegrityInspector(clean).assertClean();
  assert.equal(report.stats.entityCount, 2);
});

test('巡检: 从反射解码产物自动采集实体图', () => {
  const User: MessageDescriptor = {
    typeName: 'User',
    fields: [
      { name: 'id', number: 1, kind: 'uint64', id: true },
      { name: 'referrerId', number: 2, kind: 'uint64', ref: 'User' },
    ],
  };
  const g = new EntityGraph();
  const msg = { $type: 'User', $unknown: [], id: 7n, referrerId: 8n } as DecodedMessage;
  const entity = g.collect(User, msg);
  assert.equal(entity?.key, 'User#7');
  assert.deepEqual(entity?.refs, [{ field: 'referrerId', targetType: 'User', targetId: '8' }]);
  assert.equal(findDanglingReferences(g).length, 1); // User#8 不存在
});

test('巡检: inspect 不修改图，可重复执行结果一致（纯函数式）', () => {
  const g = buildGraph();
  const inspector = new IntegrityInspector(g);
  const r1 = JSON.stringify(inspector.inspect());
  const r2 = JSON.stringify(inspector.inspect());
  assert.equal(r1, r2);
  assert.equal(g.entities.size, 2);
});
