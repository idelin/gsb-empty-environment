import assert from 'node:assert/strict';
import type { ScrollTransaction } from '../src/types.ts';
import { vecEq } from '../src/types.ts';

/**
 * 事务守恒校验：
 *  - 每层逐轴 received = consumed + passed + discarded
 *  - 层间连续：上层 passed = 下层 received（轴变换时等于 preTransform）
 *  - 末层 passed = 事务逃逸量
 *  - 无轴变换时全事务守恒：initialDelta = Σconsumed + Σdiscarded + escaped
 *  - 同一节点在同一事务中至多出现一次（增量不被重复消费）
 *  - 轴变换至多应用一次
 */
export function assertConservation(tx: ScrollTransaction): void {
  assert.ok(tx.layers.length > 0, `tx#${tx.id}: no layers`);
  const first = tx.layers[0];
  assert.ok(
    vecEq(tx.initialDelta, first.preTransform ?? first.received),
    `tx#${tx.id}: initialDelta != first layer input`,
  );
  for (let i = 0; i < tx.layers.length; i++) {
    const layer = tx.layers[i];
    for (const axis of ['x', 'y'] as const) {
      const sum = layer.consumed[axis].add(layer.passed[axis]).add(layer.discarded[axis]);
      assert.ok(
        sum.eq(layer.received[axis]),
        `tx#${tx.id} ${layer.nodeId}.${axis}: received ${layer.received[axis]} != ` +
          `consumed ${layer.consumed[axis]} + passed ${layer.passed[axis]} + discarded ${layer.discarded[axis]}`,
      );
    }
    const next = tx.layers[i + 1];
    if (next) {
      assert.ok(
        vecEq(layer.passed, next.preTransform ?? next.received),
        `tx#${tx.id}: ${layer.nodeId}.passed != ${next.nodeId}.received`,
      );
    } else {
      assert.ok(vecEq(layer.passed, tx.escaped), `tx#${tx.id}: last passed != escaped`);
    }
  }
  const transformed = tx.layers.filter((l) => l.preTransform !== null);
  assert.ok(transformed.length <= 1, `tx#${tx.id}: axis transform applied ${transformed.length} times`);
  if (transformed.length === 0) {
    for (const axis of ['x', 'y'] as const) {
      let total = tx.escaped[axis];
      for (const layer of tx.layers) {
        total = total.add(layer.consumed[axis]).add(layer.discarded[axis]);
      }
      assert.ok(
        total.eq(tx.initialDelta[axis]),
        `tx#${tx.id}.${axis}: initialDelta != Σconsumed + Σdiscarded + escaped`,
      );
    }
  }
  const ids = tx.layers.map((l) => l.nodeId);
  assert.strictEqual(new Set(ids).size, ids.length, `tx#${tx.id}: node revisited`);
}
